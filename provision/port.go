package provision

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/gatework/set"

	"github.com/gatework/fabric"
	"github.com/gatework/fabric/device"
	"github.com/gatework/fabric/numbering"
)

// portFree rejects a port the intent or the switch already uses: a fabric
// link, a peer-link, an attachment, or (when observed) a routed address,
// a bundle membership or a vPC role. An observed switch must have the port.
func portFree(r *fabric.Region, obs *Observation, sw, port string) error {
	for _, l := range r.Links {
		if l.Uses(sw, port) {
			return fmt.Errorf("%w: %s %s is a fabric link", ErrInUse, sw, port)
		}
	}
	for _, p := range r.VPCPairs {
		for _, m := range p.Members {
			if m.Switch == sw && slices.Contains(m.PeerLinkPorts, port) {
				return fmt.Errorf("%w: %s %s is a vPC peer-link", ErrInUse, sw, port)
			}
		}
	}
	for _, a := range r.Attachments {
		if slices.Contains(a.Ports(sw), port) {
			return fmt.Errorf("%w: %s %s is assigned to %s", ErrInUse, sw, port, a.Key())
		}
	}
	cfg, ok := obs.config(sw)
	if !ok {
		return nil
	}
	i, ok := cfg.Interfaces[port]
	_, kind := device.CanonicalInterface(port)
	switch {
	case !ok && kind == device.KindPortChannel:
		return nil
	case !ok:
		return fmt.Errorf("%w: %s has no %s", ErrNotFound, sw, port)
	case len(i.Addresses) > 0:
		return fmt.Errorf("%w: %s %s is routed (%s)", ErrInUse, sw, port, i.Addresses[0])
	case i.ChannelGroup != 0:
		return fmt.Errorf("%w: %s %s is a member of port-channel%d", ErrInUse, sw, port, i.ChannelGroup)
	case i.PeerLink || i.VPC != 0:
		return fmt.Errorf("%w: %s %s carries a vPC", ErrInUse, sw, port)
	default:
		return nil
	}
}

func portChannelPool(r *fabric.Region) *set.Set[uint16] {
	if r.Underlay != nil && r.Underlay.Pools.PortChannels != nil {
		return r.Underlay.Pools.PortChannels
	}
	return set.New(set.Range[uint16]{Min: 1, Max: 4096})
}

// lowestPortChannel returns the lowest bundle number free on every switch.
func lowestPortChannel(r *fabric.Region, obs *Observation, switches []string) (int, error) {
	used := set.New[uint16]()
	mark := func(n int) { used.Add(uint16(n), uint16(n)) }
	for _, a := range r.Attachments {
		if a.Kind != fabric.AttachPort && slices.ContainsFunc(a.Switches(), func(s string) bool { return slices.Contains(switches, s) }) {
			mark(a.PortChannel)
		}
		if a.Kind == fabric.AttachPort && slices.Contains(switches, a.Switch) {
			if _, kind := device.CanonicalInterface(a.Interface); kind == device.KindPortChannel {
				mark(portNumber(a.Interface))
			}
		}
	}
	for _, p := range r.VPCPairs {
		if p.PortChannel != 0 && slices.ContainsFunc(p.Names(), func(s string) bool { return slices.Contains(switches, s) }) {
			mark(p.PortChannel)
		}
	}
	for _, sw := range switches {
		if cfg, ok := obs.config(sw); ok {
			for name, i := range cfg.Interfaces {
				if i.Kind == device.KindPortChannel {
					mark(portNumber(name))
				}
			}
		}
	}
	n, err := numbering.Lowest(portChannelPool(r), used)
	return int(n), err
}

func portNumber(name string) int {
	n := 0
	for _, c := range strings.TrimPrefix(name, "port-channel") {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(c-'0')
	}
	return n
}

// AssignPort assigns host ports: one port, an LACP bundle on one switch,
// or an LACP bundle across both members of a vPC pair. Networks the ports
// need are extended to the switches that lack them.
type AssignPort struct {
	Kind fabric.AttachmentKind
	// Switch is the port's switch, or any member of the vPC pair.
	Switch    string
	Interface string
	// Members lists bundle ports per switch.
	Members map[string][]string
	// PortChannel is allocated when zero.
	PortChannel int
	Profile     string
	Description string
	Mode        fabric.PortMode
	Networks    []fabric.VLAN
	MTU         int
	Edge        bool
}

// Apply validates, allocates and assigns.
func (q AssignPort) Apply(in *fabric.Intent, obs *Observation) (Result, error) {
	r, _, err := regionOf(in, q.Switch)
	if err != nil {
		return Result{}, err
	}
	res := Result{Region: r.Name}
	a := &fabric.Attachment{Kind: q.Kind, Switch: q.Switch, Profile: q.Profile, Description: q.Description,
		Mode: q.Mode, Networks: slices.Clone(q.Networks), MTU: q.MTU, Edge: q.Edge, PortChannel: q.PortChannel}
	if err := q.ports(r, obs, a, &res); err != nil {
		return res, err
	}
	if err := extendNetworks(r, a, &res); err != nil {
		return res, err
	}
	r.Attachments = append(r.Attachments, a)
	spec := r.Spec(a)
	res.change("attachments["+a.Key()+"]", "分配 %s：%s %s%s", a.Key(), spec.Mode, vlanText(spec.Networks), descText(spec.Description))
	res.affect(a.Switches()...)
	return res, nil
}

func vlanText(vlans []fabric.VLAN) string {
	s := numbering.Of[uint16]()
	for _, v := range vlans {
		s.Add(uint16(v), uint16(v))
	}
	return "VLAN " + s.Compact()
}

func descText(d string) string {
	if d == "" {
		return ""
	}
	return "，描述 " + d
}

func (q AssignPort) ports(r *fabric.Region, obs *Observation, a *fabric.Attachment, res *Result) error {
	switch q.Kind {
	case fabric.AttachPort:
		port, _ := device.CanonicalInterface(q.Interface)
		a.Interface = port
		return portFree(r, obs, q.Switch, port)
	case fabric.AttachPortChannel:
		if len(q.Members) != 1 || len(q.Members[q.Switch]) == 0 {
			return fmt.Errorf("%w: a port-channel needs members on %s only", ErrConflict, q.Switch)
		}
	case fabric.AttachVPC:
		pair, ok := r.Pair(q.Switch)
		if !ok {
			return fmt.Errorf("%w: %s is not in a vPC pair", ErrConflict, q.Switch)
		}
		a.Switch = pair.Members[0].Switch
		for _, name := range pair.Names() {
			if len(q.Members[name]) == 0 {
				return fmt.Errorf("%w: a vPC needs members on %s", ErrConflict, name)
			}
		}
		if len(q.Members) != 2 {
			return fmt.Errorf("%w: a vPC has members on exactly the two peers", ErrConflict)
		}
	}
	a.Members = map[string][]string{}
	for _, sw := range slices.Sorted(maps.Keys(q.Members)) {
		for _, m := range q.Members[sw] {
			port, _ := device.CanonicalInterface(m)
			if err := portFree(r, obs, sw, port); err != nil {
				return err
			}
			a.Members[sw] = append(a.Members[sw], port)
		}
	}
	if a.PortChannel == 0 {
		po, err := lowestPortChannel(r, obs, a.Switches())
		if err != nil {
			return err
		}
		a.PortChannel = po
		res.allocate("port-channel", po)
	}
	for _, sw := range a.Switches() {
		if err := portFree(r, obs, sw, fabric.PortChannelName(a.PortChannel)); err != nil {
			return err
		}
	}
	return nil
}

// extendNetworks places the attachment's networks on its switches when a
// network is limited to other switches.
func extendNetworks(r *fabric.Region, a *fabric.Attachment, res *Result) error {
	spec := r.Spec(a)
	if len(spec.Networks) == 0 {
		return fmt.Errorf("%w: the port carries no network", ErrConflict)
	}
	for _, vlan := range spec.Networks {
		n, ok := r.Network(vlan)
		if !ok {
			return fmt.Errorf("%w: network %s", ErrNotFound, vlan)
		}
		for _, name := range a.Switches() {
			s, ok := r.Switch(name)
			if !ok || r.Carries(n, s) || len(n.Switches) == 0 {
				continue
			}
			n.Switches = append(n.Switches, name)
			slices.Sort(n.Switches)
			res.change("networks["+vlan.String()+"]", "网络 %s 扩展到 %s（端口需要）", vlan, name)
		}
	}
	return nil
}

// ReleasePort returns an attachment's ports to factory state. Naming any
// port of a bundle (a member or the port-channel) releases the bundle.
type ReleasePort struct {
	Switch    string
	Interface string
}

// Apply validates and releases.
func (q ReleasePort) Apply(in *fabric.Intent, _ *Observation) (Result, error) {
	r, _, err := regionOf(in, q.Switch)
	if err != nil {
		return Result{}, err
	}
	res := Result{Region: r.Name}
	port, _ := device.CanonicalInterface(q.Interface)
	i := slices.IndexFunc(r.Attachments, func(a *fabric.Attachment) bool { return slices.Contains(a.Ports(q.Switch), port) })
	if i < 0 {
		return res, fmt.Errorf("%w: no attachment uses %s %s", ErrNotFound, q.Switch, port)
	}
	a := r.Attachments[i]
	r.Attachments = slices.Delete(r.Attachments, i, i+1)
	for _, sw := range a.Switches() {
		res.reset(sw, a.Ports(sw)...)
	}
	res.change("attachments["+a.Key()+"]", "回收 %s，端口恢复出厂并关闭", a.Key())
	return res, nil
}

// FreePort is a port that can be assigned.
type FreePort struct {
	Interface string `json:"interface"`
	Status    string `json:"status"`
}

// Free lists the switch's Ethernet ports that nothing uses, with their
// link status; ports that are up or described come last.
func Free(in *fabric.Intent, obs *Observation, name string) ([]FreePort, error) {
	r, _, err := regionOf(in, name)
	if err != nil {
		return nil, err
	}
	cfg, ok := obs.config(name)
	if !ok {
		return nil, fmt.Errorf("%w: no observation of %s", ErrNotFound, name)
	}
	st, _ := obs.state(name)
	status := map[string]string{}
	for _, i := range st.Interfaces {
		status[i.Name] = i.Status
	}
	var quiet, busy []FreePort
	for _, port := range slices.Sorted(maps.Keys(cfg.Interfaces)) {
		i := cfg.Interfaces[port]
		if i.Kind != device.KindEthernet || strings.Contains(port, ".") || portFree(r, obs, name, port) != nil {
			continue
		}
		fp := FreePort{Interface: port, Status: status[port]}
		if fp.Status == "up" || i.Description != "" {
			busy = append(busy, fp)
		} else {
			quiet = append(quiet, fp)
		}
	}
	return append(quiet, busy...), nil
}
