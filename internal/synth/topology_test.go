package synth_test

import (
	"strings"
	"testing"

	"github.com/gatework/fabric/topology"
)

func linkOf(a topology.Audit, name string) (topology.LinkCheck, bool) {
	for _, c := range a.Links {
		if c.Name == name {
			return c, true
		}
	}
	return topology.LinkCheck{}, false
}

func uplinksOf(a topology.Audit, sw string) topology.Uplinks {
	for _, u := range a.Uplinks {
		if u.Switch == sw {
			return u
		}
	}
	return topology.Uplinks{}
}

const (
	leafSpine1 = "R00-LEF001 Ethernet1/49 ↔ R00-SPN001 Ethernet1/1"
	leafSpine2 = "R00-LEF001 Ethernet1/50 ↔ R00-SPN002 Ethernet1/1"
)

func TestTopologyLayersOnCleanFabric(t *testing.T) {
	t.Parallel()
	f := small()
	a := topology.Run(f.Intent.Regions[0], assemble(f))
	if len(a.Faults()) != 0 || len(a.AtRisk()) != 0 || len(a.Unobserved) != 0 {
		t.Fatalf("clean fabric: faults %v, at risk %v, unobserved %v", a.Faults(), a.AtRisk(), a.Unobserved)
	}
	c, ok := linkOf(a, leafSpine1)
	if !ok {
		t.Fatal("link not audited")
	}
	for _, p := range c.Layers {
		if p.Verdict != topology.VerdictOK {
			t.Errorf("%s = %s (%s)", p.Layer, p.Verdict, p.Evidence)
		}
	}
	x := c.Ends[0]
	if x.Layer != "L3" || !x.Up || x.Address == "" || x.Address != x.Intended || x.Speed == "" {
		t.Errorf("end = %+v", x)
	}
	if u := uplinksOf(a, "R00-LEF001"); u.Redundancy != topology.RedundancyFull || u.Healthy != 2 {
		t.Errorf("uplinks = %+v", u)
	}
	for _, s := range a.SpineLoads {
		if s.Healthy != s.Declared || s.Declared == 0 {
			t.Errorf("spine load = %+v", s)
		}
	}
}

func TestTopologyDiagnosesLowestFailingLayer(t *testing.T) {
	t.Parallel()
	f := small()
	leaf := outputsOf(f, "R00-LEF001")
	// Uplink 1 loses its cable: the port is down and CDP and OSPF are gone.
	leaf["show interface brief"] = strings.Replace(leaf["show interface brief"],
		"Eth1/49          --      eth  routed up      none   ", "Eth1/49          --      eth  routed down    Link not connected", 1)
	leaf["show cdp neighbors detail"] = dropCDP(leaf["show cdp neighbors detail"], "Ethernet1/49")
	spine := outputsOf(f, "R00-SPN001")
	spine["show cdp neighbors detail"] = dropCDPFrom(spine["show cdp neighbors detail"], "R00-LEF001")
	leaf["show ip ospf neighbors"] = strings.Replace(leaf["show ip ospf neighbors"], "FULL/ -  1w0d 10.100.0.2 Eth1/49", "INIT/ -  1w0d 10.100.0.2 Eth1/49", 1)
	// Uplink 2 keeps its cable, but OSPF is stuck.
	leaf["show ip ospf neighbors"] = strings.Replace(leaf["show ip ospf neighbors"], "FULL/ -  1w0d 10.100.0.6 Eth1/50", "EXSTART/ -  1w0d 10.100.0.6 Eth1/50", 1)
	a := topology.Run(f.Intent.Regions[0], assemble(f))

	c, _ := linkOf(a, leafSpine1)
	if c.Diagnosis == nil || c.Diagnosis.Fault != topology.LayerPhysical || !strings.Contains(c.Diagnosis.Cause, "Link not connected") {
		t.Fatalf("cut cable diagnosis = %+v, layers %+v", c.Diagnosis, c.Layers)
	}
	if c.Ends[0].Up || c.Ends[0].Reason != "Link not connected" {
		t.Errorf("down end = %+v", c.Ends[0])
	}
	c, _ = linkOf(a, leafSpine2)
	if c.Diagnosis == nil || c.Diagnosis.Fault != topology.LayerRouting || !strings.Contains(c.Diagnosis.Cause, "EXSTART") {
		t.Fatalf("stuck OSPF diagnosis = %+v", c.Diagnosis)
	}
	if faults := a.Faults(); len(faults) != 2 || faults[0].Diagnosis.Fault != topology.LayerPhysical {
		t.Errorf("faults not ordered by layer: %+v", faults)
	}
	if u := uplinksOf(a, "R00-LEF001"); u.Redundancy != topology.RedundancyIsolated || len(u.Lost) != 2 {
		t.Errorf("uplinks = %+v", u)
	}
}

func TestTopologyFindsSinglePathAndConfigFaults(t *testing.T) {
	t.Parallel()
	f := small()
	leaf := outputsOf(f, "R00-LEF001")
	leaf["show running-config"] = strings.Replace(leaf["show running-config"], "interface Ethernet1/50\n  no switchport\n", "interface Ethernet1/50\n  switchport\n", 1)
	leaf["show ip ospf neighbors"] = strings.Replace(leaf["show ip ospf neighbors"], " 10.0.0.202 1 FULL/ -  1w0d 10.100.0.6 Eth1/50\n", "", 1)
	two := outputsOf(f, "R00-LEF002")
	two["show running-config"] = strings.Replace(two["show running-config"], "ip address 10.100.1.1/30", "ip address 10.100.1.9/30", 1)
	a := topology.Run(f.Intent.Regions[0], assemble(f))

	c, _ := linkOf(a, leafSpine2)
	if c.Diagnosis == nil || c.Diagnosis.Fault != topology.LayerConfig || !strings.Contains(c.Diagnosis.Cause, "二层") || c.Ends[0].Layer != "L2" {
		t.Fatalf("switchport diagnosis = %+v, end %+v", c.Diagnosis, c.Ends[0])
	}
	if u := uplinksOf(a, "R00-LEF001"); u.Redundancy != topology.RedundancySinglePath || u.Healthy != 1 {
		t.Errorf("uplinks = %+v", u)
	}
	var addr *topology.LinkCheck
	for i, c := range a.Links {
		if c.A.Switch == "R00-LEF002" && c.A.Interface == "Ethernet1/49" {
			addr = &a.Links[i]
		}
	}
	if addr == nil {
		t.Fatal("R00-LEF002 Ethernet1/49 not audited")
	}
	if addr.Diagnosis == nil || addr.Diagnosis.Fault != topology.LayerConfig || !strings.Contains(addr.Diagnosis.Cause, "意图为") {
		t.Errorf("address diagnosis = %+v, ends %+v", addr.Diagnosis, addr.Ends)
	}
}

func TestTopologyReportsUnobservedSwitches(t *testing.T) {
	t.Parallel()
	f := small()
	switches := assemble(f)
	a := topology.Run(f.Intent.Regions[0], switches[1:])
	if len(a.Unobserved) != 1 || a.Unobserved[0] != switches[0].Name {
		t.Fatalf("unobserved = %v", a.Unobserved)
	}
}

// dropCDP removes the CDP entry seen on a local port.
func dropCDP(text, port string) string {
	return keepEntries(text, func(e string) bool { return !strings.Contains(e, "Interface: "+port+",") })
}

// dropCDPFrom removes CDP entries for a neighbor.
func dropCDPFrom(text, neighbor string) string {
	return keepEntries(text, func(e string) bool { return !strings.Contains(e, "Device ID:"+neighbor) })
}

func keepEntries(text string, keep func(string) bool) string {
	const sep = "----------------------------------------"
	parts := strings.Split(text, sep)
	out := parts[:1]
	for _, p := range parts[1:] {
		if keep(p) {
			out = append(out, p)
		}
	}
	return strings.Join(out, sep)
}

func TestTopologyFlagsAsymmetricUplinks(t *testing.T) {
	t.Parallel()
	f := small()
	leaf := outputsOf(f, "R00-LEF001")
	leaf["show interface brief"] = strings.Replace(leaf["show interface brief"],
		"Eth1/50          --      eth  routed up      none                      100G(D)", "Eth1/50          --      eth  routed up      none                      40G(D) ", 1)
	a := topology.Run(f.Intent.Regions[0], assemble(f))
	asym := a.Asymmetric()
	if len(asym) != 1 || asym[0].Switch != "R00-LEF001" || len(asym[0].Speeds) != 2 {
		t.Fatalf("asymmetric = %+v", asym)
	}
}

func TestTopologyNeighborsAndSessions(t *testing.T) {
	t.Parallel()
	f := small()
	leaf := outputsOf(f, "R00-LEF001")
	leaf["show cdp neighbors detail"] += "----------------------------------------\nDevice ID:NAS-01\nSystem Name: NAS-01\n\nInterface address(es):\n    IPv4 Address: 192.0.2.50\nPlatform: FAS2720, Capabilities: Host\nInterface: Ethernet1/7, Port ID (outgoing port): e0M\n"
	a := topology.Run(f.Intent.Regions[0], assemble(f))
	kinds := map[string]topology.NeighborKind{}
	for _, n := range a.Neighbors {
		if n.Switch == "R00-LEF001" {
			kinds[n.Port+" "+n.Device] = n.Kind
		}
	}
	if kinds["Ethernet1/49 R00-SPN001"] != topology.NeighborFabricLink || kinds["Ethernet1/7 NAS-01"] != topology.NeighborExternal {
		t.Fatalf("neighbor kinds = %v", kinds)
	}
	u := uplinksOf(a, "R00-LEF001")
	if len(u.BGP) != 2 {
		t.Fatalf("sessions = %+v", u.BGP)
	}
	for _, s := range u.BGP {
		if s.Verdict != topology.VerdictOK {
			t.Errorf("session to %s = %+v", s.Spine, s)
		}
	}
}
