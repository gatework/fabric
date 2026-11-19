package endpoint

import (
	"cmp"
	"fmt"
	"maps"
	"net/netip"
	"slices"
	"strings"

	"github.com/gatework/fabric"
	"github.com/gatework/fabric/device"
)

// Location is one place a host is seen.
type Location struct {
	Region string      `json:"region"`
	Switch string      `json:"switch"`
	VLAN   fabric.VLAN `json:"vlan,omitempty"`
	VNI    fabric.VNI  `json:"vni,omitempty"`
	MAC    fabric.MAC  `json:"mac"`
	IP     string      `json:"ip,omitempty"`
	// Local is set where the host is attached; Port is then its port.
	Local bool   `json:"local"`
	Port  string `json:"port,omitempty"`
	// VTEP and Owners name the remote VTEP and the switches behind it.
	VTEP     string   `json:"vtep,omitempty"`
	Owners   []string `json:"owners,omitempty"`
	Producer string   `json:"producer,omitempty"`
	Sequence int      `json:"sequence"`
	Flags    string   `json:"flags,omitempty"`
}

// Region is the evidence of one region.
type Region struct {
	Name     string
	Switches []*device.Switch
	// Peers maps each vPC member to its peer.
	Peers map[string]string
}

// Index locates hosts across regions.
type Index struct {
	byMAC   map[fabric.MAC][]Location
	byLocal map[string][]Location
	byIP    map[string][]fabric.MAC
	owners  map[string][]string
	peers   map[string]string
}

// Build indexes every switch whose tables were collected.
func Build(regions []Region) *Index {
	x := &Index{byMAC: map[fabric.MAC][]Location{}, byLocal: map[string][]Location{}, byIP: map[string][]fabric.MAC{}, owners: map[string][]string{}, peers: map[string]string{}}
	for _, r := range regions {
		maps.Copy(x.peers, r.Peers)
		for _, sw := range r.Switches {
			if sw.Config == nil {
				continue
			}
			if a, ok := sw.Config.VTEPAddress(); ok {
				x.owners[a.String()] = append(x.owners[a.String()], sw.Name)
			}
			if a, ok := sw.Config.PIPAddress(); ok && a.String() != "" {
				x.owners[a.String()] = appendOnce(x.owners[a.String()], sw.Name)
			}
		}
	}
	for _, r := range regions {
		for _, sw := range r.Switches {
			x.add(r.Name, sw)
		}
	}
	for _, locs := range x.byMAC {
		for _, l := range locs {
			if l.Local {
				x.byLocal[l.Switch] = append(x.byLocal[l.Switch], l)
			}
		}
	}
	for ip, macs := range x.byIP {
		slices.Sort(macs)
		x.byIP[ip] = slices.Compact(macs)
	}
	return x
}

func appendOnce(list []string, s string) []string {
	if slices.Contains(list, s) {
		return list
	}
	return append(list, s)
}

func vniOf(cfg *device.Config, topology string) (fabric.VLAN, fabric.VNI) {
	v, err := fabric.ParseVLAN(topology)
	if err != nil || cfg == nil {
		return 0, 0
	}
	return v, cfg.VLANs[v].VNI
}

// localPorts maps MACs learned on the switch's own ports to the port.
func localPorts(sw *device.Switch) map[fabric.MAC]string {
	out := map[fabric.MAC]string{}
	for _, e := range sw.State.MACs {
		if e.RemoteVTEP == "" && e.Port != "" && !strings.Contains(e.Port, " ") {
			out[e.MAC] = e.Port
		}
	}
	for _, e := range sw.State.L2Routes {
		if e.Producer == "Local" && len(e.NextHops) > 0 {
			out[e.MAC], _ = device.CanonicalInterface(e.NextHops[0])
		}
	}
	return out
}

// Result answers one query.
type Result struct {
	Query     string       `json:"query"`
	MACs      []fabric.MAC `json:"macs"`
	Attached  []Location   `json:"attached"`
	Remote    []Location   `json:"remote"`
	Anomalies []string     `json:"anomalies,omitempty"`
}

// Lookup finds a MAC (any common notation) or an IP address.
func (x *Index) Lookup(query string) Result {
	res := Result{Query: query}
	if mac, err := fabric.ParseMAC(query); err == nil {
		res.MACs = []fabric.MAC{mac}
	} else if a, err := netip.ParseAddr(query); err == nil {
		res.MACs = x.byIP[a.String()]
	}
	for _, m := range res.MACs {
		for _, l := range x.byMAC[m] {
			if l.Local {
				res.Attached = append(res.Attached, l)
			} else {
				res.Remote = append(res.Remote, l)
			}
		}
	}
	sortLocations(res.Attached)
	sortLocations(res.Remote)
	res.Anomalies = x.anomalies(res)
	return res
}

func sortLocations(ls []Location) {
	slices.SortFunc(ls, func(a, b Location) int {
		return cmp.Or(cmp.Compare(a.Region, b.Region), cmp.Compare(a.Switch, b.Switch), cmp.Compare(a.MAC, b.MAC))
	})
}

func joinMACs(ms []fabric.MAC) string {
	parts := make([]string, len(ms))
	for i, m := range ms {
		parts[i] = string(m)
	}
	return strings.Join(parts, "、")
}

// DuplicateIPs lists IPs announced with more than one MAC.
func (x *Index) DuplicateIPs() map[string][]fabric.MAC {
	out := map[string][]fabric.MAC{}
	for ip, macs := range x.byIP {
		if len(macs) > 1 && ip != "" {
			out[ip] = macs
		}
	}
	return out
}

// Count returns the number of distinct MACs and IPs indexed.
func (x *Index) Count() (macs, ips int) { return len(x.byMAC), len(x.byIP) }

// AttachedOn returns the MACs attached locally on the switch in vlan, or
// on any VLAN when vlan is zero; port, when set, limits to that port.
func (x *Index) AttachedOn(sw string, vlan fabric.VLAN, port string) int {
	seen := map[fabric.MAC]bool{}
	for _, l := range x.byLocal[sw] {
		if (vlan == 0 || l.VLAN == vlan) && (port == "" || l.Port == port) {
			seen[l.MAC] = true
		}
	}
	return len(seen)
}

func (x *Index) add(region string, sw *device.Switch) {
	seen := map[fabric.MAC]bool{}
	ports := localPorts(sw)
	for _, e := range sw.State.MACIPs {
		vlan, vni := vniOf(sw.Config, e.Topology)
		loc := Location{Region: region, Switch: sw.Name, VLAN: vlan, VNI: vni, MAC: e.MAC, IP: e.IP,
			Producer: e.Producer, Sequence: e.Sequence, Flags: e.Flags}
		x.place(&loc, e.NextHops, e.Producer)
		if loc.Local && loc.Port == "" {
			loc.Port = ports[e.MAC]
		}
		x.byMAC[e.MAC] = append(x.byMAC[e.MAC], loc)
		x.byIP[e.IP] = append(x.byIP[e.IP], e.MAC)
		seen[e.MAC] = true
	}
	for _, e := range sw.State.L2Routes {
		if seen[e.MAC] {
			continue
		}
		vlan, vni := vniOf(sw.Config, e.Topology)
		loc := Location{Region: region, Switch: sw.Name, VLAN: vlan, VNI: vni, MAC: e.MAC,
			Producer: e.Producer, Sequence: e.Sequence, Flags: e.Flags}
		x.place(&loc, e.NextHops, e.Producer)
		x.byMAC[e.MAC] = append(x.byMAC[e.MAC], loc)
		seen[e.MAC] = true
	}
	for _, e := range sw.State.MACs {
		if seen[e.MAC] || e.VLAN == 0 {
			continue
		}
		loc := Location{Region: region, Switch: sw.Name, VLAN: e.VLAN, MAC: e.MAC, Producer: "mac-table"}
		if sw.Config != nil {
			loc.VNI = sw.Config.VLANs[e.VLAN].VNI
		}
		if e.RemoteVTEP != "" {
			loc.VTEP, loc.Owners = e.RemoteVTEP, x.owners[e.RemoteVTEP]
		} else {
			loc.Local, loc.Port = true, e.Port
		}
		x.byMAC[e.MAC] = append(x.byMAC[e.MAC], loc)
	}
}

// place marks a route local (attached here) or remote (behind a VTEP).
func (x *Index) place(loc *Location, hops []string, producer string) {
	switch {
	case producer == "Local" || producer == "HMM":
		loc.Local = true
		if len(hops) > 0 && hops[0] != "Local" {
			loc.Port, _ = device.CanonicalInterface(hops[0])
		}
	case len(hops) > 0:
		loc.VTEP, loc.Owners = hops[0], x.owners[hops[0]]
	}
}

func (x *Index) anomalies(res Result) []string {
	var out []string
	if len(res.MACs) > 1 {
		out = append(out, fmt.Sprintf("IP 对应 %d 个 MAC：%s", len(res.MACs), joinMACs(res.MACs)))
	}
	for _, m := range res.MACs {
		if sites := x.localSites(m); len(sites) > 1 {
			out = append(out, fmt.Sprintf("MAC %s 在非 vPC 对端的多台交换机本地学习：%s", m, strings.Join(sites, "、")))
		}
		for _, l := range x.byMAC[m] {
			if strings.Contains(l.Flags, "Dup") || strings.Contains(l.Flags, "PF") {
				out = append(out, fmt.Sprintf("MAC %s 在 %s 被 EVPN 标记为重复/冻结（%s）", m, l.Switch, l.Flags))
				break
			}
		}
	}
	return out
}

// localSites groups the switches that learned m locally, a vPC pair
// counting as one site.
func (x *Index) localSites(m fabric.MAC) []string {
	sites := map[string]bool{}
	for _, l := range x.byMAC[m] {
		if !l.Local {
			continue
		}
		site := l.Switch
		if p, ok := x.peers[l.Switch]; ok && p < site {
			site = p + "+" + site
		} else if ok {
			site = site + "+" + p
		}
		sites[site] = true
	}
	return slices.Sorted(maps.Keys(sites))
}

// DuplicateIPs finds IPs announced with more than one MAC straight from
// the switches' MAC-IP tables, without building an index.
func DuplicateIPs(switches []*device.Switch) map[string][]fabric.MAC {
	byIP := map[string]fabric.MAC{}
	out := map[string][]fabric.MAC{}
	for _, sw := range switches {
		for _, e := range sw.State.MACIPs {
			if e.IP == "" {
				continue
			}
			first, seen := byIP[e.IP]
			switch {
			case !seen:
				byIP[e.IP] = e.MAC
			case first != e.MAC:
				if !slices.Contains(out[e.IP], first) {
					out[e.IP] = append(out[e.IP], first)
				}
				if !slices.Contains(out[e.IP], e.MAC) {
					out[e.IP] = append(out[e.IP], e.MAC)
				}
			}
		}
	}
	for ip := range out {
		slices.Sort(out[ip])
	}
	return out
}
