package topology

import (
	"cmp"
	"slices"
	"strings"

	"github.com/gatework/fabric"
	"github.com/gatework/fabric/device"
)

// NeighborKind classifies a CDP neighbor by what the adjacency is for.
type NeighborKind uint8

// Neighbor kinds.
const (
	// NeighborExternal is a device outside the fabric: a server, storage,
	// a firewall, a router.
	NeighborExternal NeighborKind = iota
	// NeighborFabricLink is the far end of a link the intent declares.
	NeighborFabricLink
	// NeighborUndeclared is a VTEP–spine adjacency no link declares.
	NeighborUndeclared
	// NeighborPeerLink is the vPC peer seen over a peer-link member.
	NeighborPeerLink
	// NeighborFabric is another fabric switch over a port no link
	// declares, such as a border pair interconnect.
	NeighborFabric
	// NeighborManagement is seen on mgmt0, the shared management segment.
	NeighborManagement
)

// String returns the Chinese label.
func (k NeighborKind) String() string {
	switch k {
	case NeighborFabricLink:
		return "fabric 互联"
	case NeighborUndeclared:
		return "未声明互联"
	case NeighborPeerLink:
		return "vPC peer-link"
	case NeighborFabric:
		return "fabric 内"
	case NeighborManagement:
		return "管理网"
	case NeighborExternal:
		return "外部设备"
	default:
		return "外部设备"
	}
}

// MarshalText encodes the label.
func (k NeighborKind) MarshalText() ([]byte, error) { return []byte(k.String()), nil }

// Neighbor is one CDP adjacency seen from a switch port.
type Neighbor struct {
	Switch     string       `json:"switch"`
	Port       string       `json:"port"`
	Device     string       `json:"device"`
	RemotePort string       `json:"remote_port"`
	Platform   string       `json:"platform,omitempty"`
	Address    string       `json:"address,omitempty"`
	Kind       NeighborKind `json:"kind"`
	// Bundle is the port-channel the local port is a member of.
	Bundle string `json:"bundle,omitempty"`
	// Speed and Up are the local port's, from "show interface brief".
	Speed string `json:"speed,omitempty"`
	Up    bool   `json:"up"`
}

// Session is a VTEP's BGP EVPN session to one spine.
type Session struct {
	Spine   string  `json:"spine"`
	Verdict Verdict `json:"verdict"`
	State   string  `json:"state,omitempty"`
	Uptime  string  `json:"uptime,omitempty"`
}

// neighbors lists every CDP adjacency of the region's switches.
func neighbors(r *fabric.Region, switches []*device.Switch, links []LinkCheck, e *evidenceSet) []Neighbor {
	declared := map[portKey]bool{}
	undeclared := map[portKey]bool{}
	for _, c := range links {
		k := map[bool]map[portKey]bool{true: undeclared, false: declared}[c.State == StateUndeclared]
		k[portKey{c.A.Switch, c.A.Interface}] = true
		k[portKey{c.B.Switch, c.B.Interface}] = true
	}
	var out []Neighbor
	for _, sw := range switches {
		if !sw.Has(device.DimCDP) {
			continue
		}
		bundles, peerLink := membership(sw)
		for _, n := range sw.State.Neighbors {
			local, _ := device.CanonicalInterface(n.LocalPort)
			remote, _ := device.CanonicalInterface(n.RemotePort)
			nb := Neighbor{Switch: sw.Name, Port: local, Device: NeighborName(n.DeviceID), RemotePort: remote,
				Platform: n.Platform, Address: cmp.Or(n.Address, n.MgmtAddress), Bundle: bundles[local]}
			if st, ok := e.ports[portKey{sw.Name, local}]; ok {
				nb.Speed, nb.Up = st.Speed, st.Up
			}
			_, inFabric := r.Switch(nb.Device)
			k := portKey{sw.Name, local}
			switch {
			case strings.HasPrefix(strings.ToLower(local), "mgmt"):
				nb.Kind = NeighborManagement
			case declared[k]:
				nb.Kind = NeighborFabricLink
			case undeclared[k]:
				nb.Kind = NeighborUndeclared
			case inFabric && nb.Bundle != "" && nb.Bundle == peerLink:
				nb.Kind = NeighborPeerLink
			case inFabric:
				nb.Kind = NeighborFabric
			default:
				nb.Kind = NeighborExternal
			}
			out = append(out, nb)
		}
	}
	slices.SortFunc(out, func(x, y Neighbor) int {
		return cmp.Or(cmp.Compare(x.Switch, y.Switch), cmp.Compare(x.Kind, y.Kind), comparePorts(x.Port, y.Port))
	})
	return out
}

// membership maps member ports to their port-channel and returns the
// vPC peer-link's port-channel.
func membership(sw *device.Switch) (map[string]string, string) {
	out := map[string]string{}
	for _, p := range sw.State.PortChannels {
		name, _ := device.CanonicalInterface(p.Name)
		for _, m := range p.Members {
			port, _ := device.CanonicalInterface(m.Port)
			out[port] = name
		}
	}
	peer := ""
	if v := sw.State.VPC; v != nil {
		for _, l := range v.PeerLinks {
			peer, _ = device.CanonicalInterface(l.Port)
		}
	}
	return out, peer
}

// comparePorts orders "Ethernet1/9" before "Ethernet1/10".
func comparePorts(a, b string) int {
	pa, pb := portNumbers(a), portNumbers(b)
	if c := cmp.Compare(strings.TrimRight(a, "0123456789/"), strings.TrimRight(b, "0123456789/")); c != 0 && (len(pa) == 0 || len(pb) == 0) {
		return c
	}
	if c := slices.Compare(pa, pb); c != 0 {
		return c
	}
	return cmp.Compare(a, b)
}

func portNumbers(s string) []int {
	var out []int
	n, in := 0, false
	for _, r := range s {
		if r >= '0' && r <= '9' {
			n, in = n*10+int(r-'0'), true
			continue
		}
		if in {
			out = append(out, n)
		}
		n, in = 0, false
	}
	if in {
		out = append(out, n)
	}
	return out
}

// sessions finds each VTEP's BGP EVPN session to every spine by the
// spine's router ID.
func sessions(r *fabric.Region, sw *device.Switch) []Session {
	var out []Session
	for _, sp := range r.ByRole(fabric.RoleSpine) {
		s := Session{Spine: sp.Name}
		if sw == nil || !sw.Has(device.DimBGP) || !sp.RouterID.IsValid() {
			out = append(out, s)
			continue
		}
		s.Verdict, s.State = VerdictFail, "无会话"
		for _, n := range sw.State.BGP.Neighbors {
			if n.Address != sp.RouterID.String() {
				continue
			}
			s.Uptime = n.UpDown
			if n.Established {
				s.Verdict, s.State = VerdictOK, "Established"
			} else {
				s.State = cmp.Or(n.State, "未建立")
			}
		}
		out = append(out, s)
	}
	return out
}
