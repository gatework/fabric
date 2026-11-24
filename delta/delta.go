// Package delta compares two collections of the fabric and reports what
// changed in between: per switch, the configuration changes block by
// block, and, when both collections hold the operational state, the
// ports that went up or down, the OSPF, BGP EVPN and NVE adjacencies lost
// or gained, and the endpoints that moved to another port.
//
// A side without a dimension is reported as not collected, never as
// unchanged. The package performs no I/O; configuration text is masked by
// the caller.
package delta

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/gatework/netops/diff"
	"github.com/gatework/netops/evidence"

	"github.com/gatework/fabric/device"
)

// Severity of a state change: a loss is what an operator looks at first.
type Severity uint8

// Severities.
const (
	SeverityInfo Severity = iota
	SeverityGain
	SeverityLoss
)

// String returns the Chinese label.
func (s Severity) String() string {
	switch s {
	case SeverityLoss:
		return "丢失"
	case SeverityGain:
		return "新增"
	case SeverityInfo:
		return "变化"
	default:
		return "变化"
	}
}

// MarshalText encodes the label.
func (s Severity) MarshalText() ([]byte, error) { return []byte(s.String()), nil }

// StateChange is one operational difference.
type StateChange struct {
	Area     string   `json:"area"`
	Severity Severity `json:"severity"`
	What     string   `json:"what"`
	Before   string   `json:"before,omitempty"`
	After    string   `json:"after,omitempty"`
}

// Section is one top-level configuration block and its changes.
type Section struct {
	Name    string        `json:"name"`
	Changes []diff.Change `json:"changes"`
}

// Switch is everything that changed on one switch.
type Switch struct {
	Name   string `json:"name"`
	Region string `json:"region,omitempty"`
	// Presence is "both", "added" (only in the later collection) or
	// "removed" (only in the earlier one).
	Presence string `json:"presence"`
	// Sections groups configuration changes by top-level block; Added
	// and Removed count lines.
	Sections []Section `json:"sections,omitempty"`
	Added    int       `json:"added"`
	Removed  int       `json:"removed"`
	// ConfigKnown is false when either side lacks the configuration.
	ConfigKnown bool          `json:"config_known"`
	State       []StateChange `json:"state,omitempty"`
	// StateKnown is false when either side lacks the operational state.
	StateKnown bool `json:"state_known"`
}

// Changed reports whether anything changed on the switch.
func (s Switch) Changed() bool {
	return s.Presence != "both" || len(s.Sections) > 0 || len(s.State) > 0
}

// Losses counts the state changes that lost something.
func (s Switch) Losses() int {
	n := 0
	for _, c := range s.State {
		if c.Severity == SeverityLoss {
			n++
		}
	}
	return n
}

// Report compares two collections.
type Report struct {
	From     string   `json:"from"`
	To       string   `json:"to"`
	Switches []Switch `json:"switches"`
}

// Changed returns the switches with changes, those with lost state first.
func (r Report) Changed() []Switch {
	var out []Switch
	for _, s := range r.Switches {
		if s.Changed() {
			out = append(out, s)
		}
	}
	slices.SortStableFunc(out, func(a, b Switch) int {
		return cmp.Or(cmp.Compare(b.Losses(), a.Losses()), cmp.Compare(b.Added+b.Removed, a.Added+a.Removed), cmp.Compare(a.Name, b.Name))
	})
	return out
}

// Pair is one switch in both collections, keyed by name, with the
// structural configuration diff the caller computed (nil when either
// configuration is missing).
type Pair struct {
	Name, Region  string
	Before, After *device.Switch
	Config        []diff.Change
	ConfigKnown   bool
}

// Compare builds the report from the switch pairs.
func Compare(from, to string, pairs []Pair) Report {
	rep := Report{From: from, To: to}
	for _, p := range pairs {
		rep.Switches = append(rep.Switches, compare(p))
	}
	slices.SortFunc(rep.Switches, func(a, b Switch) int { return cmp.Or(cmp.Compare(a.Region, b.Region), cmp.Compare(a.Name, b.Name)) })
	return rep
}

func compare(p Pair) Switch {
	s := Switch{Name: p.Name, Region: p.Region, Presence: "both", ConfigKnown: p.ConfigKnown}
	switch {
	case p.Before == nil:
		s.Presence = "added"
		return s
	case p.After == nil:
		s.Presence = "removed"
		return s
	}
	s.Sections = sections(p.Config)
	for _, c := range p.Config {
		n := max(1, len(c.Block))
		switch c.Kind {
		case diff.KindAdded:
			s.Added += n
		case diff.KindRemoved:
			s.Removed += n
		case diff.KindChanged:
			s.Added++
			s.Removed++
		}
	}
	s.State, s.StateKnown = state(p.Before, p.After)
	return s
}

func sections(changes []diff.Change) []Section {
	var out []Section
	index := map[string]int{}
	for _, c := range changes {
		name := c.Line
		if len(c.Path) > 0 {
			name = c.Path[0]
		}
		i, ok := index[name]
		if !ok {
			i = len(out)
			index[name] = i
			out = append(out, Section{Name: name})
		}
		out[i].Changes = append(out[i].Changes, c)
	}
	return out
}

// Kinds summarizes sections by their first word: "interface ×5".
func (s Switch) Kinds() string {
	count := map[string]int{}
	var order []string
	for _, sec := range s.Sections {
		k, _, _ := strings.Cut(sec.Name, " ")
		if count[k] == 0 {
			order = append(order, k)
		}
		count[k]++
	}
	parts := make([]string, 0, len(order))
	for _, k := range order {
		if count[k] > 1 {
			parts = append(parts, fmt.Sprintf("%s ×%d", k, count[k]))
		} else {
			parts = append(parts, k)
		}
	}
	return strings.Join(parts, "，")
}

type stateDim struct {
	dim  evidence.Dimension
	area string
	set  func(*device.Switch) map[string]string
}

var stateDims = []stateDim{
	{device.DimInterfaces, "端口", portStates},
	{device.DimOSPF, "OSPF", ospfFull},
	{device.DimBGP, "BGP EVPN", bgpEstablished},
	{device.DimNVEPeers, "NVE 对端", nveUp},
}

// state compares the operational state both sides observed.
func state(a, b *device.Switch) ([]StateChange, bool) {
	var out []StateChange
	known := false
	for _, d := range stateDims {
		if !a.Has(d.dim) || !b.Has(d.dim) {
			continue
		}
		// A dimension the configuration proves absent (NVE on a spine) is
		// complete but says nothing about state having been collected.
		known = known || d.dim != device.DimNVEPeers
		out = append(out, compareSets(d.area, d.set(a), d.set(b))...)
	}
	if a.Has(device.DimMACs) && b.Has(device.DimMACs) {
		known = true
		out = append(out, moved(a, b)...)
	}
	return out, known
}

// compareSets reports keys whose value changed, appeared or vanished. An
// empty value means "not up": a key going to "" is a loss.
func compareSets(area string, before, after map[string]string) []StateChange {
	var out []StateChange
	keys := slices.Sorted(maps.Keys(before))
	for k := range after {
		if _, ok := before[k]; !ok {
			keys = append(keys, k)
		}
	}
	slices.SortFunc(keys, device.ComparePorts)
	keys = slices.Compact(keys)
	for _, k := range keys {
		x, inA := before[k]
		y, inB := after[k]
		switch {
		case inA && inB && x == y:
		case inA && (!inB || y == ""):
			if x != "" {
				out = append(out, StateChange{Area: area, Severity: SeverityLoss, What: k, Before: x, After: cmp.Or(y, "无")})
			}
		case (!inA || x == "") && inB && y != "":
			out = append(out, StateChange{Area: area, Severity: SeverityGain, What: k, Before: cmp.Or(x, "无"), After: y})
		default:
			out = append(out, StateChange{Area: area, Severity: SeverityInfo, What: k, Before: x, After: y})
		}
	}
	return out
}

// portStates maps Ethernet and port-channel ports to "up", or "" when
// they are not up; other states are not interesting here.
func portStates(sw *device.Switch) map[string]string {
	out := map[string]string{}
	for _, p := range sw.State.Interfaces {
		name, kind := device.CanonicalInterface(p.Name)
		if kind != device.KindEthernet && kind != device.KindPortChannel {
			continue
		}
		if p.Up {
			out[name] = "up"
		} else {
			out[name] = ""
		}
	}
	return out
}

func ospfFull(sw *device.Switch) map[string]string {
	out := map[string]string{}
	for _, n := range sw.State.OSPF {
		if n.Full {
			out[n.RouterID] = "FULL " + n.Interface
		}
	}
	return out
}

func bgpEstablished(sw *device.Switch) map[string]string {
	out := map[string]string{}
	for _, n := range sw.State.BGP.Neighbors {
		if n.Established {
			out[n.Address] = "Established"
		}
	}
	return out
}

func nveUp(sw *device.Switch) map[string]string {
	out := map[string]string{}
	for _, p := range sw.State.NVEPeers {
		if p.Up {
			out[p.Peer] = "Up"
		}
	}
	return out
}

// moved reports endpoints learned locally on both sides but on another
// port, and summarizes those that appeared or vanished.
func moved(a, b *device.Switch) []StateChange {
	local := func(sw *device.Switch) map[string]string {
		out := map[string]string{}
		for _, m := range sw.State.MACs {
			if m.RemoteVTEP != "" {
				continue
			}
			port, kind := device.CanonicalInterface(m.Port)
			if kind == device.KindEthernet || kind == device.KindPortChannel {
				out[string(m.MAC)] = port
			}
		}
		return out
	}
	before, after := local(a), local(b)
	var out []StateChange
	gone, came := 0, 0
	for _, mac := range slices.Sorted(maps.Keys(before)) {
		switch p, ok := after[mac]; {
		case !ok:
			gone++
		case p != before[mac]:
			out = append(out, StateChange{Area: "终端", Severity: SeverityInfo, What: mac + " 换端口", Before: before[mac], After: p})
		}
	}
	for mac := range after {
		if _, ok := before[mac]; !ok {
			came++
		}
	}
	if gone > 0 {
		out = append(out, StateChange{Area: "终端", Severity: SeverityLoss, What: fmt.Sprintf("%d 个本地终端不再出现", gone)})
	}
	if came > 0 {
		out = append(out, StateChange{Area: "终端", Severity: SeverityGain, What: fmt.Sprintf("%d 个本地终端新出现", came)})
	}
	return out
}
