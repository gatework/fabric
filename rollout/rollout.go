package rollout

import (
	"cmp"
	"fmt"
	"maps"
	"slices"

	"github.com/gatework/fabric"
	"github.com/gatework/fabric/device"
)

// Member is one switch to change.
type Member struct {
	Name    string      `json:"name"`
	Region  string      `json:"region"`
	Role    fabric.Role `json:"role"`
	VPCPeer string      `json:"vpc_peer,omitempty"`
}

// Strategy tunes scheduling.
type Strategy struct {
	// WaveSize caps how many leaves change together; zero means 4.
	WaveSize int `json:"wave_size"`
	// Canary changes one leaf alone before anything else; it is on unless
	// NoCanary is set.
	NoCanary bool `json:"no_canary,omitempty"`
}

// Wave is a set of switches changed concurrently.
type Wave struct {
	Index    int      `json:"index"`
	Label    string   `json:"label"`
	Switches []Member `json:"switches"`
}

// Schedule orders members into waves. Within a wave no two members are vPC
// peers; borders and spines are alone in their waves; spines come last.
func Schedule(members []Member, s Strategy) []Wave {
	size := s.WaveSize
	if size <= 0 {
		size = 4
	}
	sorted := slices.Clone(members)
	slices.SortFunc(sorted, func(a, b Member) int {
		return cmp.Or(cmp.Compare(a.Region, b.Region), cmp.Compare(a.Name, b.Name))
	})
	var leaves, borders, spines []Member
	for _, m := range sorted {
		switch m.Role {
		case fabric.RoleLeaf:
			leaves = append(leaves, m)
		case fabric.RoleBorder:
			borders = append(borders, m)
		case fabric.RoleSpine, fabric.RoleUnset:
			spines = append(spines, m)
		}
	}
	var waves []Wave
	add := func(label string, ms []Member) {
		if len(ms) > 0 {
			waves = append(waves, Wave{Index: len(waves) + 1, Label: label, Switches: ms})
		}
	}
	if !s.NoCanary && len(leaves) > 0 {
		canary := pickCanary(leaves)
		add("canary", []Member{leaves[canary]})
		leaves = slices.Delete(leaves, canary, canary+1)
	}
	for _, w := range leafWaves(leaves, size) {
		add("leaf "+w[0].Region, w)
	}
	for _, b := range borders {
		add("border "+b.Region, []Member{b})
	}
	for _, sp := range spines {
		add("spine "+sp.Region, []Member{sp})
	}
	return waves
}

// pickCanary prefers a leaf outside any vPC pair, so the canary's failure
// cannot isolate dual-homed hosts.
func pickCanary(leaves []Member) int {
	if i := slices.IndexFunc(leaves, func(m Member) bool { return m.VPCPeer == "" }); i >= 0 {
		return i
	}
	return 0
}

// leafWaves splits leaves per region into waves of at most size members
// with vPC peers in different waves: first members of pairs and single
// leaves first, then second members.
func leafWaves(leaves []Member, size int) [][]Member {
	byRegion := map[string][]Member{}
	for _, m := range leaves {
		byRegion[m.Region] = append(byRegion[m.Region], m)
	}
	var out [][]Member
	for _, region := range slices.Sorted(maps.Keys(byRegion)) {
		var first, second []Member
		placed := map[string]bool{}
		for _, m := range byRegion[region] {
			if m.VPCPeer != "" && placed[m.VPCPeer] {
				second = append(second, m)
			} else {
				first = append(first, m)
			}
			placed[m.Name] = true
		}
		out = append(out, chunk(first, size)...)
		out = append(out, chunk(second, size)...)
	}
	return out
}

func chunk(ms []Member, size int) [][]Member {
	var out [][]Member
	for len(ms) > 0 {
		n := min(size, len(ms))
		out = append(out, ms[:n:n])
		ms = ms[n:]
	}
	return out
}

// Validate reports waves that put vPC peers together.
func Validate(waves []Wave) error {
	for _, w := range waves {
		names := map[string]bool{}
		for _, m := range w.Switches {
			names[m.Name] = true
		}
		for _, m := range w.Switches {
			if m.VPCPeer != "" && names[m.VPCPeer] {
				return fmt.Errorf("rollout: wave %d changes vPC peers %s and %s together", w.Index, m.Name, m.VPCPeer)
			}
		}
	}
	return nil
}

// Health is the operational summary a gate compares.
type Health struct {
	NVEPeers    []string `json:"nve_peers"`
	BGPSessions []string `json:"bgp_sessions"`
	OSPF        []string `json:"ospf"`
	VPCPeerOK   bool     `json:"vpc_peer_ok"`
	HasVPC      bool     `json:"has_vpc"`
}

// HealthOf summarizes a switch's operational state.
func HealthOf(st device.State) Health {
	h := Health{}
	for _, p := range st.NVEPeers {
		if p.Up {
			h.NVEPeers = append(h.NVEPeers, p.Peer)
		}
	}
	for _, n := range st.BGP.Neighbors {
		if n.Established {
			h.BGPSessions = append(h.BGPSessions, n.Address)
		}
	}
	for _, n := range st.OSPF {
		if n.Full {
			h.OSPF = append(h.OSPF, n.Process+"/"+n.RouterID)
		}
	}
	if st.VPC != nil {
		h.HasVPC, h.VPCPeerOK = true, st.VPC.PeerOK && st.VPC.KeepaliveOK
	}
	slices.Sort(h.NVEPeers)
	slices.Sort(h.BGPSessions)
	slices.Sort(h.OSPF)
	return h
}

// Gate returns what the switch lost between before and after. An empty
// result means the rollout may continue.
func Gate(before, after Health) []string {
	var out []string
	for _, l := range []struct {
		what string
		b, a []string
	}{{"nve peer", before.NVEPeers, after.NVEPeers}, {"evpn session", before.BGPSessions, after.BGPSessions}, {"ospf neighbor", before.OSPF, after.OSPF}} {
		for _, x := range l.b {
			if !slices.Contains(l.a, x) {
				out = append(out, l.what+" "+x+" lost")
			}
		}
	}
	if before.HasVPC && before.VPCPeerOK && !after.VPCPeerOK {
		out = append(out, "vpc peer lost")
	}
	return out
}
