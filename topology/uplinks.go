package topology

import (
	"slices"
	"strings"

	"github.com/gatework/fabric"
	"github.com/gatework/fabric/device"
)

// Redundancy is how many independent paths a VTEP keeps to the spines.
type Redundancy uint8

// Redundancy levels.
const (
	RedundancyUnknown Redundancy = iota
	// RedundancyFull: every declared uplink is healthy.
	RedundancyFull
	// RedundancyDegraded: some uplinks failed, but the VTEP still reaches
	// at least one spine; one more failure may isolate it.
	RedundancyDegraded
	// RedundancySinglePath: exactly one healthy uplink is left.
	RedundancySinglePath
	// RedundancyIsolated: no healthy uplink is left.
	RedundancyIsolated
)

// String returns the Chinese label.
func (r Redundancy) String() string {
	switch r {
	case RedundancyFull:
		return "冗余正常"
	case RedundancyDegraded:
		return "冗余降级"
	case RedundancySinglePath:
		return "单上行"
	case RedundancyIsolated:
		return "孤立"
	case RedundancyUnknown:
		return "未知"
	default:
		return "未知"
	}
}

// MarshalText encodes the label.
func (r Redundancy) MarshalText() ([]byte, error) { return []byte(r.String()), nil }

// AtRisk reports whether the VTEP lost redundancy.
func (r Redundancy) AtRisk() bool { return r >= RedundancyDegraded }

// Uplinks is a VTEP's paths to the spines.
type Uplinks struct {
	Switch   string      `json:"switch"`
	Role     fabric.Role `json:"role"`
	Declared int         `json:"declared"`
	Healthy  int         `json:"healthy"`
	// Spines are the spines reached over a healthy uplink; Lost are the
	// spines the VTEP declares uplinks to but reaches over none.
	Spines     []string   `json:"spines"`
	Lost       []string   `json:"lost,omitempty"`
	Redundancy Redundancy `json:"redundancy"`
	// Speeds are the distinct speeds of the healthy uplinks. More than one
	// means ECMP spreads flows evenly over unequal links, so the slower
	// one congests first.
	Speeds []string `json:"speeds,omitempty"`
}

// Asymmetric reports whether the healthy uplinks run at different speeds.
func (u Uplinks) Asymmetric() bool { return len(u.Speeds) > 1 }

// SpineLoad is a spine's downlinks: how much of the fabric depends on it.
type SpineLoad struct {
	Switch   string `json:"switch"`
	Declared int    `json:"declared"`
	Healthy  int    `json:"healthy"`
}

func (a *Audit) redundancy(r *fabric.Region, e *evidenceSet) {
	spines := map[string]*SpineLoad{}
	for _, s := range r.ByRole(fabric.RoleSpine) {
		spines[s.Name] = &SpineLoad{Switch: s.Name}
	}
	for _, v := range r.VTEPs() {
		u := Uplinks{Switch: v.Name, Role: v.Role}
		reached, declared := map[string]bool{}, map[string]bool{}
		unknown := 0
		for i := range a.Links {
			c := &a.Links[i]
			sp, ok := otherEnd(c, v.Name)
			if !ok {
				continue
			}
			load, isSpine := spines[sp]
			if !isSpine || c.State == StateUndeclared {
				continue
			}
			u.Declared++
			load.Declared++
			declared[sp] = true
			switch {
			case c.State == StateUnknown:
				unknown++
			case c.Healthy():
				u.Healthy++
				load.Healthy++
				reached[sp] = true
				if s := speedOf(c, v.Name); s != "" && !slices.Contains(u.Speeds, s) {
					u.Speeds = append(u.Speeds, s)
				}
			}
		}
		for sp := range declared {
			if reached[sp] {
				u.Spines = append(u.Spines, sp)
			} else if unknown == 0 {
				u.Lost = append(u.Lost, sp)
			}
		}
		slices.Sort(u.Spines)
		slices.Sort(u.Lost)
		u.Redundancy = level(u, unknown, e.observed(v.Name, device.DimCDP))
		a.Uplinks = append(a.Uplinks, u)
	}
	for _, s := range r.ByRole(fabric.RoleSpine) {
		a.SpineLoads = append(a.SpineLoads, *spines[s.Name])
	}
}

func level(u Uplinks, unknown int, seen bool) Redundancy {
	switch {
	case u.Declared == 0 || (!seen && u.Healthy == 0) || unknown == u.Declared:
		return RedundancyUnknown
	case u.Healthy == 0:
		return RedundancyIsolated
	case u.Healthy == 1 && u.Declared > 1:
		return RedundancySinglePath
	case u.Healthy+unknown < u.Declared:
		return RedundancyDegraded
	default:
		return RedundancyFull
	}
}

func otherEnd(c *LinkCheck, sw string) (string, bool) {
	switch sw {
	case c.A.Switch:
		return c.B.Switch, true
	case c.B.Switch:
		return c.A.Switch, true
	default:
		return "", false
	}
}

// speedOf returns the speed of the link's end on the switch, without the
// "(D)" duplex marker of "show interface brief".
func speedOf(c *LinkCheck, sw string) string {
	for _, e := range c.Ends {
		if e.Switch == sw {
			s, _, _ := strings.Cut(e.Speed, "(")
			return s
		}
	}
	return ""
}
