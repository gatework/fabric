package synth

import (
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gatework/fabric"
	"github.com/gatework/fabric/adapter/snapshot"
	"github.com/gatework/fabric/vendors/nxos"
)

// Spec sizes a synthetic fabric.
type Spec struct {
	Regions  int
	Spines   int // per region
	Leaves   int // per region; consecutive pairs form vPC pairs
	Networks int // per region, at most 80
	// LocalHosts is the number of hosts learned locally per leaf.
	LocalHosts int
	// RemoteHosts is how many remote hosts each leaf's tables show.
	RemoteHosts int
}

// Thousand is 10 regions of 2 spines and 98 leaves: 1000 switches.
func Thousand() Spec {
	return Spec{Regions: 10, Spines: 2, Leaves: 98, Networks: 40, LocalHosts: 30, RemoteHosts: 570}
}

// Fleet is a generated fabric.
type Fleet struct {
	Intent *fabric.Intent
	// Outputs holds command outputs per management address.
	Outputs map[netip.Addr]map[string]string
}

// Generate builds the fabric.
func Generate(s Spec) *Fleet {
	f := &Fleet{Intent: &fabric.Intent{Schema: fabric.IntentSchema}, Outputs: map[netip.Addr]map[string]string{}}
	for r := range s.Regions {
		g := &gen{spec: s, r: r, fleet: f}
		g.build()
	}
	return f
}

// Write lays the outputs out as a snapshot directory under root.
func (f *Fleet) Write(root string) error {
	commands := make([]string, 0, len(nxos.Probes()))
	for _, p := range nxos.Probes() {
		commands = append(commands, p.Command)
	}
	for addr, outs := range f.Outputs {
		files, err := snapshot.Encode(addr.String(), commands, outs, "", time.Unix(0, 0))
		if err != nil {
			return err
		}
		for rel, body := range files {
			p := filepath.Join(root, rel)
			if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
				return err
			}
			if err := os.WriteFile(p, body, 0o600); err != nil {
				return err
			}
		}
	}
	return nil
}

type gen struct {
	spec  Spec
	r     int
	fleet *Fleet
}

func addr(format string, args ...any) netip.Addr {
	return netip.MustParseAddr(fmt.Sprintf(format, args...))
}

func (g *gen) name(role string, i int) string { return fmt.Sprintf("R%02d-%s%03d", g.r, role, i+1) }
func (g *gen) mgmt(i int) netip.Addr          { return addr("172.16.%d.%d", g.r, i+1) }
func (g *gen) lo0(i int) netip.Addr           { return addr("10.%d.0.%d", g.r, i+1) }
func (g *gen) lo1(i int) netip.Addr           { return addr("10.%d.1.%d", g.r, i+1) }
func (g *gen) anycast(pair int) netip.Addr    { return addr("10.%d.2.%d", g.r, pair+1) }

// linkEnd returns the leaf (0) or spine (1) end of leaf l's link to spine s.
func (g *gen) linkEnd(l, s, end int) netip.Prefix {
	return netip.MustParsePrefix(fmt.Sprintf("10.%d.%d.%d/30", 100+g.r, l, s*4+1+end))
}

func (g *gen) build() {
	s := g.spec
	r := &fabric.Region{
		Name:      fmt.Sprintf("region-%02d", g.r),
		Fabric:    fabric.Settings{ASN: fabric.ASN(65000 + g.r), AnycastGatewayMAC: "0000.2222.3333", SVIMTU: 9216, SuppressARP: true},
		Numbering: fabric.Numbering{L2VNIBase: 10000},
		Underlay: &fabric.Underlay{OSPFProcess: "10", OSPFArea: "0.0.0.0", MTU: 9216, LinkPrefix: 30,
			BGP: fabric.Peering{VTEPTemplate: "SPINES", SpineTemplate: "VTEPS"},
			Pools: fabric.Pools{RouterID: fabric.MustParsePrefix(fmt.Sprintf("10.%d.0.0/24", g.r)),
				VTEP: fabric.MustParsePrefix(fmt.Sprintf("10.%d.1.0/24", g.r)), AnycastVTEP: fabric.MustParsePrefix(fmt.Sprintf("10.%d.2.0/24", g.r)),
				Links: fabric.MustParsePrefix(fmt.Sprintf("10.%d.0.0/16", 100+g.r))}},
		Tenants: []*fabric.Tenant{{Name: "PROD", L3VNI: 50000, VLAN: 999}},
	}

	for i := range s.Spines {
		r.Switches = append(r.Switches, &fabric.Switch{Name: g.name("SPN", i), Mgmt: fabric.AddrOf(g.mgmt(200 + i)), Role: fabric.RoleSpine,
			RouterID: fabric.AddrOf(g.lo0(200 + i))})
	}
	for i := range s.Leaves {
		r.Switches = append(r.Switches, &fabric.Switch{Name: g.name("LEF", i), Mgmt: fabric.AddrOf(g.mgmt(i)), Role: fabric.RoleLeaf,
			RouterID: fabric.AddrOf(g.lo0(i)), VTEP: fabric.AddrOf(g.lo1(i))})
		for sp := range s.Spines {
			r.Links = append(r.Links, &fabric.Link{
				A: fabric.LinkEnd{Switch: g.name("LEF", i), Interface: fmt.Sprintf("Ethernet1/%d", 49+sp), Address: fabric.PrefixOf(g.linkEnd(i, sp, 0))},
				B: fabric.LinkEnd{Switch: g.name("SPN", sp), Interface: fmt.Sprintf("Ethernet1/%d", i+1), Address: fabric.PrefixOf(g.linkEnd(i, sp, 1))},
			})
		}
		if i%2 == 1 {
			r.VPCPairs = append(r.VPCPairs, &fabric.VPCPair{Domain: i/2 + 1, AnycastVTEP: fabric.AddrOf(g.anycast(i / 2)),
				Members: [2]fabric.VPCMember{{Switch: g.name("LEF", i-1)}, {Switch: g.name("LEF", i)}}})
		}
	}
	for k := range s.Networks {
		r.Networks = append(r.Networks, &fabric.Network{Name: fmt.Sprintf("net%d", k), VLAN: fabric.VLAN(100 + k), Tenant: "PROD",
			Gateway: fabric.MustParsePrefix(fmt.Sprintf("10.%d.%d.1/24", g.r, 10+k))})
	}
	g.fleet.Intent.Regions = append(g.fleet.Intent.Regions, r)
	for i := range s.Spines {
		g.fleet.Outputs[g.mgmt(200+i)] = g.spine(i)
	}
	for i := range s.Leaves {
		g.fleet.Outputs[g.mgmt(i)] = g.leaf(i)
	}
}

func (g *gen) mac(leaf, host int) string {
	return fmt.Sprintf("52%02x.%04x.%04x", g.r, leaf, host)
}

func (g *gen) spine(i int) map[string]string {
	s := g.spec
	var c strings.Builder
	fmt.Fprintf(&c, "version 10.4(2)\nhostname %s\nfeature ospf\nfeature bgp\n", g.name("SPN", i))
	for l := range s.Leaves {
		fmt.Fprintf(&c, "interface Ethernet1/%d\n  no switchport\n  mtu 9216\n  ip address %s\n  ip ospf network point-to-point\n  ip router ospf 10 area 0.0.0.0\n  no shutdown\n", l+1, g.linkEnd(l, i, 1))
	}
	fmt.Fprintf(&c, "interface loopback0\n  ip address %s/32\n  ip router ospf 10 area 0.0.0.0\n", g.lo0(200+i))
	fmt.Fprintf(&c, "router ospf 10\n  router-id %s\n", g.lo0(200+i))
	fmt.Fprintf(&c, "router bgp %d\n  router-id %s\n  template peer VTEPS\n    remote-as %d\n    update-source loopback0\n    address-family l2vpn evpn\n      send-community\n      send-community extended\n      route-reflector-client\n  neighbor 10.%d.0.0/24\n    inherit peer VTEPS\n",
		65000+g.r, g.lo0(200+i), 65000+g.r, g.r)
	var bgp, ospf, cdp strings.Builder
	fmt.Fprintf(&bgp, "BGP router identifier %s, local AS number %d\nBGP table version is 1, L2VPN EVPN config peers 1, capable peers %d\n\nNeighbor        V    AS    MsgRcvd    MsgSent   TblVer  InQ OutQ Up/Down  State/PfxRcd\n", g.lo0(200+i), 65000+g.r, s.Leaves)
	_, _ = fmt.Fprintf(&ospf, " OSPF Process ID 10 VRF default\n Total number of neighbors: %d\n Neighbor ID     Pri State            Up Time  Address         Interface\n", s.Leaves)
	for l := range s.Leaves {
		fmt.Fprintf(&bgp, "%s 4 %d 100 100 1 0 0 1w0d 50\n", g.lo0(l), 65000+g.r)
		fmt.Fprintf(&ospf, " %s 1 FULL/ -  1w0d %s Eth1/%d\n", g.lo0(l), g.linkEnd(l, i, 0).Addr(), l+1)
		fmt.Fprintf(&cdp, "----------------------------------------\nDevice ID:%s(FDO%07d)\nPlatform: N9K-C93180YC-FX, Capabilities: Router Switch\nInterface: Ethernet1/%d, Port ID (outgoing port): Ethernet1/%d\n", g.name("LEF", l), l, l+1, 49+i)
	}
	return map[string]string{
		"show running-config": c.String(), "show bgp l2vpn evpn summary": bgp.String(),
		"show ip ospf neighbors": ospf.String(), "show cdp neighbors detail": cdp.String(),
		"show interface brief":      "Port   VRF          Status IP Address\nmgmt0  --           up     " + g.mgmt(200+i).String() + " 1000 1500\n",
		"show port-channel summary": "Group Port-       Type     Protocol  Member Ports\n",
		"show mac address-table":    "   VLAN     MAC Address      Type      age     Secure NTFY Ports\n",
	}
}
