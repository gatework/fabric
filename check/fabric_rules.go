package check

import (
	"fmt"
	"maps"
	"net/netip"
	"slices"
	"strings"

	"github.com/gatework/netops/assess"
	"github.com/gatework/netops/drift"
	"github.com/gatework/netops/evidence"

	"github.com/gatework/fabric"
	"github.com/gatework/fabric/baseline"
	"github.com/gatework/fabric/device"
	"github.com/gatework/fabric/endpoint"
	"github.com/gatework/fabric/plan"
	"github.com/gatework/fabric/topology"
)

// Rule identifiers of the fabric catalog.
const (
	RuleVLANMapping      assess.RuleID = "fabric.vlan_vni_mapping"
	RuleVNIGateway       assess.RuleID = "fabric.vni_gateway"
	RuleAnycastMAC       assess.RuleID = "fabric.anycast_gateway_mac"
	RuleL3VNI            assess.RuleID = "fabric.l3vni_mapping"
	RuleVTEPReachability assess.RuleID = "fabric.vtep_reachability"
	RuleRRSessions       assess.RuleID = "fabric.route_reflector_sessions"
	RuleAddressUnique    assess.RuleID = "fabric.address_unique"
	RuleTemplateNames    assess.RuleID = "fabric.bgp_template_names"
	RuleIntentDrift      assess.RuleID = "fabric.intent_drift"
	RuleBaselineDrift    assess.RuleID = "fabric.baseline_drift"
	RuleRegression       assess.RuleID = "fabric.regression"
	RuleLinkMissing      assess.RuleID = "fabric.link_missing"
	RuleLinkMiscabled    assess.RuleID = "fabric.link_miscabled"
	RuleLinkUndeclared   assess.RuleID = "fabric.link_undeclared"
	RuleLinkInconsistent assess.RuleID = "fabric.link_inconsistent"
	RuleLeafSpineMesh    assess.RuleID = "fabric.leaf_spine_mesh"
	RuleDuplicateIP      assess.RuleID = "fabric.endpoint_duplicate_ip"
)

type fabricEval func(f *Fabric, sub assess.Subject) []assess.Finding

func fabricRule(id assess.RuleID, title string, sev assess.Severity, cat assess.Category, needs []evidence.Dimension, eval fabricEval) assess.Rule[*Fabric] {
	return assess.Check(assess.Meta{ID: id, Title: title, Severity: sev, Category: cat, Requires: needs, Verify: needs},
		func(in assess.Input[*Fabric]) []assess.Finding {
			return eval(in.Model, fabricSubject(in.Model))
		})
}

// FabricRules returns the fabric catalog in evaluation order.
func FabricRules() []assess.Rule[*Fabric] {
	high, med, low := assess.SeverityHigh, assess.SeverityMedium, assess.SeverityLow
	cons, rt, dr := assess.CategoryConsistency, assess.CategoryRuntime, assess.CategoryDrift
	cfg := []evidence.Dimension{device.DimConfig}
	cdp := []evidence.Dimension{device.DimConfig, device.DimCDP}
	return []assess.Rule[*Fabric]{
		fabricRule(RuleVLANMapping, "同一 VLAN 在 VTEP 间映射到不同 VNI", high, cons, cfg, vlanMapping),
		fabricRule(RuleVNIGateway, "同一 VNI 的 anycast 网关或 VRF 不一致", high, cons, cfg, vniGateway),
		fabricRule(RuleAnycastMAC, "Anycast 网关 MAC 不一致", high, cons, cfg, anycastMAC),
		fabricRule(RuleL3VNI, "同一租户 VRF 的 L3VNI 不一致", high, cons, cfg, l3vniMapping),
		fabricRule(RuleVTEPReachability, "VTEP 未学到全部对端", high, rt, []evidence.Dimension{device.DimConfig, device.DimNVEPeers}, vtepReachability),
		fabricRule(RuleRRSessions, "VTEP 未与全部路由反射器建立 EVPN 会话", high, rt, []evidence.Dimension{device.DimConfig, device.DimBGP}, rrSessions),
		fabricRule(RuleAddressUnique, "Router-ID 或 VTEP 地址重复", high, cons, cfg, addressUnique),
		fabricRule(RuleTemplateNames, "BGP peer 模板命名不一致", low, cons, cfg, templateNames),
		fabricRule(RuleIntentDrift, "运行配置偏离意图", med, dr, cfg, intentDrift),
		fabricRule(RuleBaselineDrift, "运行配置偏离基线", med, dr, cfg, baselineDrift),
		fabricRule(RuleRegression, "相比上次采集出现运行状态退化", high, dr, []evidence.Dimension{device.DimNVEPeers, device.DimBGP, device.DimOSPF}, regression),
		fabricRule(RuleLinkMissing, "意图互联在 CDP 中不可见", high, rt, cdp, linkState(topology.StateMissing)),
		fabricRule(RuleLinkMiscabled, "互联错接", high, cons, cdp, linkState(topology.StateMiscabled)),
		fabricRule(RuleLinkUndeclared, "存在意图未声明的 leaf–spine 互联", low, cons, cdp, linkState(topology.StateUndeclared)),
		fabricRule(RuleLinkInconsistent, "互联两端配置不一致（MTU/OSPF/子网）", high, cons, cfg, linkMismatch),
		fabricRule(RuleLeafSpineMesh, "VTEP 未与全部 spine 互联", med, cons, cfg, unmeshed),
		fabricRule(RuleDuplicateIP, "同一 IP 被多个 MAC 宣告", high, rt, []evidence.Dimension{device.DimMACIPs}, duplicateIPs),
		poolRule(),
	}
}

// outliers reports, for each key, the members that hold a minority value.
func outliers[K comparable, V comparable](f *Fabric, sub func(K) assess.Subject, values map[K]map[string]V, describe func(K, V, []string) string) []assess.Finding {
	var out []assess.Finding
	for k, byMember := range values {
		var members []drift.Member[V]
		for _, who := range slices.Sorted(maps.Keys(byMember)) {
			members = append(members, drift.Member[V]{ID: who, Value: byMember[who]})
		}
		if len(members) < 2 {
			continue
		}
		a := drift.Consensus(members, func(v V) string { return fmt.Sprintf("%#v", v) }, 0)
		if len(a.Outliers) == 0 {
			continue
		}
		want, ok := a.Value, true
		devs := a.Outliers
		if a.Tied || a.Count < 2 {
			// No clear majority: every member is in question.
			var zero V
			want, ok, devs = zero, false, members
		}
		var who []string
		for _, d := range devs {
			who = append(who, d.ID)
		}
		var expected any
		if ok {
			expected = want
		}
		out = append(out, assess.Finding{Subject: sub(k), Detail: describe(k, want, who), Expected: text(expected), Actual: text(who)})
	}
	slices.SortFunc(out, bySubject)
	return out
}

func vlanMapping(f *Fabric, sub assess.Subject) []assess.Finding {
	values := map[fabric.VLAN]map[string]fabric.VNI{}
	for _, sw := range f.VTEPs() {
		for v, def := range sw.Config.VLANs {
			if def.VNI == 0 {
				continue
			}
			if values[v] == nil {
				values[v] = map[string]fabric.VNI{}
			}
			values[v][sw.Name] = def.VNI
		}
	}
	return outliers(f, func(v fabric.VLAN) assess.Subject { return sub.Child("vlan", v.String()) }, values,
		func(v fabric.VLAN, want fabric.VNI, who []string) string {
			return fmt.Sprintf("VLAN %s 多数映射到 VNI %s，以下交换机不同：%s", v, want, strings.Join(who, ", "))
		})
}

type gateway struct {
	VRF     string
	Address netip.Prefix
}

func vniGateway(f *Fabric, sub assess.Subject) []assess.Finding {
	values := map[fabric.VNI]map[string]gateway{}
	for _, sw := range f.VTEPs() {
		for _, seg := range sw.Config.Segments() {
			if seg.Layer != device.LayerL2 || seg.SVI == nil || !seg.SVI.Anycast {
				continue
			}
			addr, _ := seg.SVI.Primary()
			if values[seg.VNI] == nil {
				values[seg.VNI] = map[string]gateway{}
			}
			values[seg.VNI][sw.Name] = gateway{VRF: seg.SVI.VRF, Address: addr}
		}
	}
	return outliers(f, func(v fabric.VNI) assess.Subject { return sub.Child("vni", v.String()) }, values,
		func(v fabric.VNI, want gateway, who []string) string {
			return fmt.Sprintf("VNI %s 多数网关为 %s（vrf %s），以下交换机不同：%s", v, want.Address, want.VRF, strings.Join(who, ", "))
		})
}

func anycastMAC(f *Fabric, sub assess.Subject) []assess.Finding {
	values := map[string]map[string]fabric.MAC{"mac": {}}
	for _, sw := range f.VTEPs() {
		if sw.Config.AnycastGatewayMAC != "" {
			values["mac"][sw.Name] = sw.Config.AnycastGatewayMAC
		}
	}
	return outliers(f, func(string) assess.Subject { return sub.Child("setting", "anycast_gateway_mac") }, values,
		func(_ string, want fabric.MAC, who []string) string {
			return fmt.Sprintf("多数 anycast 网关 MAC 为 %s，以下交换机不同：%s", want, strings.Join(who, ", "))
		})
}

func l3vniMapping(f *Fabric, sub assess.Subject) []assess.Finding {
	values := map[string]map[string]fabric.VNI{}
	for _, sw := range f.VTEPs() {
		for name, v := range sw.Config.VRFs {
			if v.VNI == 0 {
				continue
			}
			if values[name] == nil {
				values[name] = map[string]fabric.VNI{}
			}
			values[name][sw.Name] = v.VNI
		}
	}
	return outliers(f, func(name string) assess.Subject { return sub.Child("vrf", name) }, values,
		func(name string, want fabric.VNI, who []string) string {
			return fmt.Sprintf("VRF %s 多数 L3VNI 为 %s，以下交换机不同：%s", name, want, strings.Join(who, ", "))
		})
}

// vtepReachability expects every VTEP to see every other VTEP address
// except its own (a vPC pair shares one anycast address).
func vtepReachability(f *Fabric, sub assess.Subject) []assess.Finding {
	addrs := map[string]string{}
	for _, sw := range f.VTEPs() {
		if a, ok := sw.Config.VTEPAddress(); ok {
			addrs[sw.Name] = a.String()
		}
	}
	all := map[string]bool{}
	for _, a := range addrs {
		all[a] = true
	}
	var out []assess.Finding
	for _, sw := range f.VTEPs() {
		own, ok := addrs[sw.Name]
		if !ok || len(sw.Config.Segments()) == 0 {
			continue
		}
		seen := map[string]bool{}
		for _, p := range sw.State.NVEPeers {
			seen[p.Peer] = true
		}
		var missing []string
		for _, a := range slices.Sorted(maps.Keys(all)) {
			if a != own && !seen[a] {
				missing = append(missing, a)
			}
		}
		if len(missing) > 0 {
			out = append(out, found(sub.Child("switch", sw.Name), fmt.Sprintf("%s 缺少 %d 个 VTEP 对端：%s", sw.Name, len(missing), strings.Join(missing, ", ")), len(all)-1, len(seen)))
		}
	}
	return out
}

// rrSessions expects every VTEP to hold an established EVPN session to
// each spine, addressed by the spine's router-id or loopback0.
func rrSessions(f *Fabric, sub assess.Subject) []assess.Finding {
	spines := map[string]string{}
	for _, sp := range f.ByRole(fabric.RoleSpine) {
		for _, a := range spineAddresses(sp.Config) {
			spines[a] = sp.Name
		}
	}
	if len(spines) == 0 {
		return nil
	}
	names := map[string]bool{}
	for _, n := range spines {
		names[n] = true
	}
	var out []assess.Finding
	for _, sw := range f.VTEPs() {
		up := map[string]bool{}
		for _, n := range sw.State.BGP.Neighbors {
			if name, ok := spines[n.Address]; ok && n.Established {
				up[name] = true
			}
		}
		var missing []string
		for _, n := range slices.Sorted(maps.Keys(names)) {
			if !up[n] {
				missing = append(missing, n)
			}
		}
		if len(missing) > 0 {
			out = append(out, found(sub.Child("switch", sw.Name), sw.Name+" 未与路由反射器建立 EVPN 会话："+strings.Join(missing, ", "), len(names), len(up)))
		}
	}
	return out
}

func spineAddresses(cfg *device.Config) []string {
	var out []string
	if cfg.BGP != nil && cfg.BGP.RouterID != "" {
		out = append(out, cfg.BGP.RouterID)
	}
	if lo, ok := cfg.Interfaces["loopback0"]; ok {
		if p, ok := lo.Primary(); ok {
			out = append(out, p.Addr().String())
		}
	}
	return out
}

func addressUnique(f *Fabric, sub assess.Subject) []assess.Finding {
	routerIDs := map[string][]string{}
	pips := map[string][]string{}
	for _, sw := range f.Switches {
		if b := sw.Config.BGP; b != nil && b.RouterID != "" {
			routerIDs[b.RouterID] = append(routerIDs[b.RouterID], sw.Name)
		}
		if a, ok := sw.Config.PIPAddress(); ok {
			pips[a.String()] = append(pips[a.String()], sw.Name)
		}
	}
	var out []assess.Finding
	for _, id := range slices.Sorted(maps.Keys(routerIDs)) {
		if who := routerIDs[id]; len(who) > 1 {
			out = append(out, found(sub.Child("router_id", id), "BGP router-id "+id+" 被多台交换机使用："+strings.Join(who, ", "), 1, len(who)))
		}
	}
	for _, a := range slices.Sorted(maps.Keys(pips)) {
		if who := pips[a]; len(who) > 1 {
			out = append(out, found(sub.Child("vtep_pip", a), "VTEP 主地址 "+a+" 被多台交换机使用："+strings.Join(who, ", "), 1, len(who)))
		}
	}
	return out
}

func templateNames(f *Fabric, sub assess.Subject) []assess.Finding {
	values := map[string]map[string]string{"templates": {}}
	for _, sw := range f.VTEPs() {
		b := sw.Config.BGP
		if b == nil {
			continue
		}
		used := map[string]bool{}
		for _, n := range b.Neighbors {
			if n.Inherit != "" {
				used[n.Inherit] = true
			}
		}
		values["templates"][sw.Name] = strings.Join(slices.Sorted(maps.Keys(used)), ",")
	}
	return outliers(f, func(string) assess.Subject { return sub.Child("bgp", "templates") }, values,
		func(_ string, want string, who []string) string {
			return fmt.Sprintf("多数 VTEP 继承模板 %q，以下交换机不同：%s", want, strings.Join(who, ", "))
		})
}

func intentDrift(f *Fabric, sub assess.Subject) []assess.Finding {
	if f.Intent == nil {
		return nil
	}
	var out []assess.Finding
	for _, sw := range f.Switches {
		s, ok := f.Intent.Switch(sw.Name)
		if !ok {
			continue
		}
		ops := plan.Diff(plan.Compile(f.Intent, s), sw.Config, plan.Options{})
		for _, op := range ops {
			out = append(out, assess.Finding{
				Subject:     sub.Child("switch", sw.Name).Child(string(op.Ref().Kind), op.Ref().Key),
				Detail:      fmt.Sprintf("%s 需要 %s %s", sw.Name, op.Action(), op.Ref()),
				Expected:    "intent",
				Actual:      op.Action().String(),
				Remediation: &assess.Remediation{Summary: fmt.Sprintf("%s %s", op.Action(), op.Ref())},
			})
		}
	}
	return out
}

func baselineDrift(f *Fabric, sub assess.Subject) []assess.Finding {
	if f.Baseline == nil {
		return nil
	}
	var out []assess.Finding
	for _, sw := range f.Switches {
		exp := f.Baseline.Resolve(f.Name, sw.Role)
		for _, d := range checkBaseline(sub.Child("switch", sw.Name), exp, sw.Config) {
			fd := d.Finding(fmt.Sprintf("%s %s：期望 %v，实际 %v", sw.Name, d.Path, orDash(d.Expected), orDash(d.Actual)))
			fd.Remediation = &assess.Remediation{Summary: "align " + d.Path + " with the baseline"}
			out = append(out, fd)
		}
	}
	return out
}

func orDash(v string) string {
	if v == "" {
		return "-"
	}
	return v
}

func regression(f *Fabric, sub assess.Subject) []assess.Finding {
	if f.Previous == nil {
		return nil
	}
	var out []assess.Finding
	for _, sw := range f.Switches {
		prev, ok := f.Previous[sw.Key()]
		if !ok {
			continue
		}
		s := sub.Child("switch", sw.Name)
		out = append(out, lost(s, "NVE 对端", upPeers(prev), upPeers(sw))...)
		out = append(out, lost(s, "EVPN 会话", establishedSessions(prev), establishedSessions(sw))...)
		out = append(out, lost(s, "OSPF 邻居", fullNeighbors(prev), fullNeighbors(sw))...)
	}
	return out
}

func lost(sub assess.Subject, what string, before, after map[string]bool) []assess.Finding {
	var gone []string
	for _, k := range slices.Sorted(maps.Keys(before)) {
		if !after[k] {
			gone = append(gone, k)
		}
	}
	if len(gone) == 0 {
		return nil
	}
	return []assess.Finding{found(sub.Child("lost", what), fmt.Sprintf("相比上次采集丢失 %d 个%s：%s", len(gone), what, strings.Join(gone, ", ")), len(before), len(after))}
}

func upPeers(sw *device.Switch) map[string]bool {
	out := map[string]bool{}
	for _, p := range sw.State.NVEPeers {
		if p.Up {
			out[p.Peer] = true
		}
	}
	return out
}

func establishedSessions(sw *device.Switch) map[string]bool {
	out := map[string]bool{}
	for _, n := range sw.State.BGP.Neighbors {
		if n.Established {
			out[n.Address] = true
		}
	}
	return out
}

func fullNeighbors(sw *device.Switch) map[string]bool {
	out := map[string]bool{}
	for _, n := range sw.State.OSPF {
		if n.Full {
			out[n.Process+"/"+n.RouterID] = true
		}
	}
	return out
}

func checkBaseline(sub assess.Subject, exp baseline.Expectation, cfg *device.Config) []drift.Deviation {
	return baseline.Check(sub, exp, cfg)
}

func linkState(want topology.State) fabricEval {
	return func(f *Fabric, sub assess.Subject) []assess.Finding {
		var out []assess.Finding
		for _, c := range f.Topology().Links {
			if c.State != want {
				continue
			}
			out = append(out, found(sub.Child("switch", c.A.Switch).Child("link", c.A.Interface),
				c.Name+"："+orText(c.Detail, c.State.String()), "connected as declared", c.State.String()))
		}
		return out
	}
}

func orText(s, alt string) string {
	if s == "" {
		return alt
	}
	return s
}

func linkMismatch(f *Fabric, sub assess.Subject) []assess.Finding {
	var out []assess.Finding
	for _, c := range f.Topology().Links {
		if len(c.Mismatch) > 0 && c.State != topology.StateUndeclared {
			out = append(out, found(sub.Child("switch", c.A.Switch).Child("link", c.A.Interface),
				c.Name+" 两端不一致："+strings.Join(c.Mismatch, "，"), "same", c.Mismatch))
		}
	}
	return out
}

func unmeshed(f *Fabric, sub assess.Subject) []assess.Finding {
	if f.Intent == nil || f.Intent.Underlay == nil {
		return nil
	}
	var out []assess.Finding
	for _, pair := range f.Topology().Unmeshed {
		out = append(out, found(sub.Child("mesh", pair), pair+" 之间没有互联", "linked", "absent"))
	}
	return out
}

func duplicateIPs(f *Fabric, sub assess.Subject) []assess.Finding {
	dups := endpoint.DuplicateIPs(f.Switches)
	var out []assess.Finding
	for _, ip := range slices.Sorted(maps.Keys(dups)) {
		macs := dups[ip]
		parts := make([]string, len(macs))
		for i, m := range macs {
			parts[i] = string(m)
		}
		out = append(out, found(sub.Child("ip", ip), fmt.Sprintf("IP %s 被 %d 个 MAC 宣告：%s", ip, len(macs), strings.Join(parts, "、")), 1, len(macs)))
	}
	return out
}
