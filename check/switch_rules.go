package check

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/gatework/go-netops/assess"
	"github.com/gatework/go-netops/evidence"

	"github.com/gatework/fabric"
	"github.com/gatework/fabric/device"
)

// Rule identifiers of the switch catalog.
const (
	RuleSegmentIncomplete   assess.RuleID = "fabric.segment_incomplete"
	RuleVNILayerConflict    assess.RuleID = "fabric.vni_layer_conflict"
	RuleSVIAttributes       assess.RuleID = "fabric.svi_attributes"
	RuleOrphanSVI           assess.RuleID = "fabric.orphan_svi"
	RuleEmptyVRF            assess.RuleID = "fabric.empty_vrf"
	RuleUnusedTemplate      assess.RuleID = "fabric.unused_bgp_template"
	RuleUndefinedPolicy     assess.RuleID = "fabric.undefined_policy"
	RuleSpineOverlay        assess.RuleID = "fabric.spine_overlay_residue"
	RuleNVEControlPlane     assess.RuleID = "fabric.nve_control_plane"
	RuleVPCOptions          assess.RuleID = "fabric.vpc_options"
	RuleVPCVTEP             assess.RuleID = "fabric.vpc_vtep"
	RuleVirtualPeerLinkTCAM assess.RuleID = "fabric.virtual_peer_link_tcam"
	RuleSNMPWeak            assess.RuleID = "fabric.snmp_weak" //nolint:gosec // G101: a rule identifier, not a credential.
	RuleNVEPeerDown         assess.RuleID = "fabric.nve_peer_down"
	RuleNVEVNIDown          assess.RuleID = "fabric.nve_vni_down"
	RuleVNINotOperational   assess.RuleID = "fabric.vni_not_operational"
	RuleBGPSessionDown      assess.RuleID = "fabric.bgp_session_down"
	RuleBGPNoPrefixes       assess.RuleID = "fabric.bgp_no_prefixes"
	RuleOSPFNotFull         assess.RuleID = "fabric.ospf_not_full"
	RuleVPCUnhealthy        assess.RuleID = "fabric.vpc_unhealthy"
	RulePortChannelDown     assess.RuleID = "fabric.port_channel_member_down"
	RuleDuplicateMAC        assess.RuleID = "fabric.duplicate_mac"
	RuleEvidence            assess.RuleID = "fabric.evidence_incomplete"
)

type switchEval func(sw *device.Switch, sub assess.Subject) []assess.Finding

func switchRule(id assess.RuleID, title string, sev assess.Severity, cat assess.Category, needs []evidence.Dimension, eval switchEval) assess.Rule[*device.Switch] {
	return assess.NewRule(assess.RuleMeta{ID: id, Title: title, Severity: sev, Category: cat, Requires: needs, Verify: needs},
		func(in assess.Input[*device.Switch]) []assess.Finding {
			return eval(in.Model, switchSubject(in.Model))
		})
}

func found(sub assess.Subject, detail string, expected, actual any) assess.Finding {
	return assess.Finding{Subject: sub, Detail: detail, Expected: expected, Actual: actual}
}

// SwitchRules returns the switch catalog in evaluation order.
func SwitchRules() []assess.Rule[*device.Switch] {
	high, med, low, crit := assess.SeverityHigh, assess.SeverityMedium, assess.SeverityLow, assess.SeverityCritical
	cfg, rt, sec := assess.CategoryConfiguration, assess.CategoryRuntime, assess.CategorySecurity
	config := []evidence.Dimension{device.DimConfig}
	return []assess.Rule[*device.Switch]{
		switchRule(RuleEvidence, "证据不完整", med, assess.CategoryEvidence, nil, evidenceGaps),
		switchRule(RuleSegmentIncomplete, "VNI 配置链不完整（VLAN/NVE/EVPN/SVI）", high, cfg, config, segmentGaps),
		switchRule(RuleVNILayerConflict, "同一 VNI 同时用作二层与三层", high, cfg, config, vniLayerConflicts),
		switchRule(RuleSVIAttributes, "Anycast 网关 SVI 属性缺失", med, cfg, config, sviAttributes),
		switchRule(RuleOrphanSVI, "孤立 SVI（无地址、无网关、无 VNI）", low, cfg, config, orphanSVIs),
		switchRule(RuleEmptyVRF, "空 VRF（无 VNI、无路由、无成员）", low, cfg, config, emptyVRFs),
		switchRule(RuleUnusedTemplate, "未被引用的 BGP peer 模板", low, cfg, config, unusedTemplates),
		switchRule(RuleUndefinedPolicy, "引用了未定义的 ACL 或 route-map", high, cfg, config, undefinedPolicies),
		switchRule(RuleSpineOverlay, "Spine 上残留 Overlay 配置", med, cfg, config, spineResidue),
		switchRule(RuleNVEControlPlane, "NVE 未使用 BGP 控制平面或未启用", high, cfg, config, nveControlPlane),
		switchRule(RuleVPCOptions, "vPC 域缺少推荐选项", med, cfg, config, vpcOptions),
		switchRule(RuleVPCVTEP, "vPC VTEP 的 Anycast VTEP / PIP 配置不一致", high, cfg, config, vpcVTEP),
		switchRule(RuleVirtualPeerLinkTCAM, "虚拟 peer-link 缺少 ing-flow-redirect TCAM 划分", high, cfg, config, virtualPeerLinkTCAM),
		switchRule(RuleSNMPWeak, "SNMP 认证或访问控制较弱", med, sec, config, snmpWeak),
		switchRule(RuleNVEPeerDown, "NVE 对端不在 Up 状态", crit, rt, []evidence.Dimension{device.DimNVEPeers}, nvePeersDown),
		switchRule(RuleNVEVNIDown, "VNI 运行状态非 Up", high, rt, []evidence.Dimension{device.DimNVEVNIs}, nveVNIsDown),
		switchRule(RuleVNINotOperational, "已配置的 VNI 未出现在 NVE 运行表中", high, rt, []evidence.Dimension{device.DimConfig, device.DimNVEVNIs}, vnisNotOperational),
		switchRule(RuleBGPSessionDown, "BGP EVPN 会话未建立", crit, rt, []evidence.Dimension{device.DimBGP}, bgpDown),
		switchRule(RuleBGPNoPrefixes, "BGP EVPN 会话未收到前缀", med, rt, []evidence.Dimension{device.DimBGP}, bgpNoPrefixes),
		switchRule(RuleOSPFNotFull, "OSPF 邻居未达到 FULL", high, rt, []evidence.Dimension{device.DimOSPF}, ospfNotFull),
		switchRule(RuleVPCUnhealthy, "vPC 对端或一致性异常", crit, rt, []evidence.Dimension{device.DimVPC}, vpcUnhealthy),
		switchRule(RulePortChannelDown, "Port-channel 成员未捆绑", med, rt, []evidence.Dimension{device.DimPortChannels}, portChannelDown),
		switchRule(RuleDuplicateMAC, "EVPN 检测到重复或冻结的 MAC", high, rt, []evidence.Dimension{device.DimL2Routes}, duplicateMACs),
	}
}

func evidenceGaps(sw *device.Switch, sub assess.Subject) []assess.Finding {
	var out []assess.Finding
	for _, d := range device.Dimensions() {
		s := sw.Coverage.Status(d)
		if s.Complete() || s == evidence.StatusNotRequested {
			continue
		}
		var codes []string
		for _, i := range sw.Issues[d] {
			codes = append(codes, i.Code)
		}
		out = append(out, found(sub.Child("dimension", string(d)), fmt.Sprintf("%s 状态为 %s %v", d, s, codes), evidence.StatusComplete, s))
	}
	return out
}

func segmentGaps(sw *device.Switch, sub assess.Subject) []assess.Finding {
	if !sw.Role.VTEP() && sw.Role.Valid() {
		return nil
	}
	var out []assess.Finding
	for _, seg := range sw.Config.Segments() {
		gaps := seg.Gaps()
		if len(gaps) == 0 {
			continue
		}
		out = append(out, found(sub.Child("vni", seg.VNI.String()),
			fmt.Sprintf("%s VNI %s 缺少 %s", seg.Layer, seg.VNI, strings.Join(gaps, "、")), "complete", gaps))
	}
	return out
}

func vniLayerConflicts(sw *device.Switch, sub assess.Subject) []assess.Finding {
	var out []assess.Finding
	for vni, vrf := range sw.Config.L3VNIs() {
		if _, ok := sw.Config.EVPN[vni]; ok {
			out = append(out, found(sub.Child("vni", vni.String()), fmt.Sprintf("VNI %s 是 VRF %s 的 L3VNI，同时配置了 evpn vni l2", vni, vrf), "l3", "l2+l3"))
		}
	}
	slices.SortFunc(out, bySubject)
	return out
}

func bySubject(a, b assess.Finding) int {
	return strings.Compare(a.Subject.String(), b.Subject.String())
}

func sortedInterfaces(cfg *device.Config, kind device.InterfaceKind) []device.Interface {
	var out []device.Interface
	for _, name := range slices.Sorted(maps.Keys(cfg.Interfaces)) {
		if i := cfg.Interfaces[name]; i.Kind == kind {
			out = append(out, i)
		}
	}
	return out
}

func sviAttributes(sw *device.Switch, sub assess.Subject) []assess.Finding {
	cfg := sw.Config
	var out []assess.Finding
	anycast := false
	for _, i := range sortedInterfaces(cfg, device.KindSVI) {
		if !i.Anycast {
			continue
		}
		anycast = true
		var missing []string
		if i.VRF == "" {
			missing = append(missing, "vrf member")
		}
		if i.Admin != device.AdminUp {
			missing = append(missing, "no shutdown")
		}
		if _, ok := i.Primary(); !ok && !i.IPForward {
			missing = append(missing, "ip address")
		}
		if len(missing) > 0 {
			out = append(out, found(sub.Child("interface", i.Name), i.Name+" 缺少 "+strings.Join(missing, "、"), "configured", missing))
		}
	}
	if anycast && cfg.AnycastGatewayMAC == "" {
		out = append(out, found(sub, "存在 anycast 网关 SVI 但未配置 fabric forwarding anycast-gateway-mac", "configured", "absent"))
	}
	return out
}

func orphanSVIs(sw *device.Switch, sub assess.Subject) []assess.Finding {
	cfg := sw.Config
	var out []assess.Finding
	for _, i := range sortedInterfaces(cfg, device.KindSVI) {
		v, err := fabric.ParseVLAN(strings.TrimPrefix(i.Name, "Vlan"))
		if err != nil || v == 1 {
			continue
		}
		_, addressed := i.Primary()
		_, vlanExists := cfg.VLANs[v]
		if addressed || i.Anycast || i.IPForward || i.VRF != "" && vlanExists && cfg.VLANs[v].VNI != 0 {
			continue
		}
		reason := "无地址、无 anycast"
		if !vlanExists {
			reason = "对应 VLAN 不存在"
		}
		out = append(out, found(sub.Child("interface", i.Name), i.Name+" 为孤立 SVI："+reason, "removed or completed", "orphan"))
	}
	return out
}

func emptyVRFs(sw *device.Switch, sub assess.Subject) []assess.Finding {
	cfg := sw.Config
	used := map[string]bool{}
	for _, i := range cfg.Interfaces {
		used[i.VRF] = true
	}
	var out []assess.Finding
	for _, name := range slices.Sorted(maps.Keys(cfg.VRFs)) {
		v := cfg.VRFs[name]
		if name == "management" || name == "default" || v.VNI != 0 || len(v.Routes) > 0 || used[name] {
			continue
		}
		out = append(out, found(sub.Child("vrf", name), "VRF "+name+" 无 VNI、无静态路由、无接口成员", "removed", "empty"))
	}
	return out
}

func unusedTemplates(sw *device.Switch, sub assess.Subject) []assess.Finding {
	b := sw.Config.BGP
	if b == nil {
		return nil
	}
	used := map[string]bool{}
	for _, p := range b.Neighbors {
		used[p.Inherit] = true
	}
	for _, v := range b.VRFs {
		for _, p := range v.Neighbors {
			used[p.Inherit] = true
		}
	}
	var out []assess.Finding
	for _, name := range slices.Sorted(maps.Keys(b.Templates)) {
		if !used[name] {
			out = append(out, found(sub.Child("bgp_template", name), "BGP 模板 "+name+" 未被任何邻居继承", "inherited", "unused"))
		}
	}
	return out
}

func undefinedPolicies(sw *device.Switch, sub assess.Subject) []assess.Finding {
	cfg := sw.Config
	var out []assess.Finding
	for _, i := range sortedInterfaces(cfg, device.KindSVI) {
		for _, acl := range []string{i.ACLIn, i.ACLOut} {
			if _, ok := cfg.ACLs[acl]; acl != "" && !ok {
				out = append(out, found(sub.Child("interface", i.Name), i.Name+" 引用未定义的 ACL "+acl, "defined", "undefined"))
			}
		}
	}
	refs := map[string]string{}
	for name, v := range cfg.VRFs {
		for _, m := range v.Exports {
			refs[m] = "vrf " + name + " export"
		}
	}
	if cfg.BGP != nil {
		for name, v := range cfg.BGP.VRFs {
			for proto, m := range v.Redistribute {
				if m != "" {
					refs[m] = "bgp vrf " + name + " redistribute " + proto
				}
			}
		}
	}
	for _, m := range slices.Sorted(maps.Keys(refs)) {
		if _, ok := cfg.RouteMaps[m]; !ok {
			out = append(out, found(sub.Child("route_map", m), refs[m]+" 引用未定义的 route-map "+m, "defined", "undefined"))
		}
	}
	return out
}

func spineResidue(sw *device.Switch, sub assess.Subject) []assess.Finding {
	if sw.Role != fabric.RoleSpine {
		return nil
	}
	cfg := sw.Config
	var out []assess.Finding
	if cfg.NVE != nil {
		out = append(out, found(sub.Child("interface", cfg.NVE.Name), fmt.Sprintf("Spine 上存在 %s（%d 个 member vni）", cfg.NVE.Name, len(cfg.NVE.Members)), "absent", "present"))
	}
	for v, def := range cfg.VLANs {
		if def.VNI != 0 {
			out = append(out, found(sub.Child("vlan", v.String()), fmt.Sprintf("Spine 上 VLAN %s 映射了 VNI %s", v, def.VNI), "absent", "present"))
		}
	}
	slices.SortFunc(out, bySubject)
	return out
}

func nveControlPlane(sw *device.Switch, sub assess.Subject) []assess.Finding {
	n := sw.Config.NVE
	if n == nil || !sw.Role.VTEP() {
		return nil
	}
	var out []assess.Finding
	if n.Shutdown {
		out = append(out, found(sub.Child("interface", n.Name), n.Name+" 处于 shutdown", "no shutdown", "shutdown"))
	}
	if !n.HostReachabilityBGP {
		out = append(out, found(sub.Child("interface", n.Name), n.Name+" 未配置 host-reachability protocol bgp", "bgp", "flood-and-learn"))
	}
	if n.SourceInterface == "" {
		out = append(out, found(sub.Child("interface", n.Name), n.Name+" 未配置 source-interface", "loopback", "absent"))
	}
	return out
}

func vpcOptions(sw *device.Switch, sub assess.Subject) []assess.Finding {
	d := sw.Config.VPC
	if d == nil {
		return nil
	}
	var missing []string
	for _, o := range []device.VPCOption{device.VPCPeerGateway, device.VPCARPSynchronize, device.VPCL3PeerRouter, device.VPCAutoRecovery, device.VPCPeerSwitch} {
		if !d.Option(o) {
			missing = append(missing, string(o))
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return []assess.Finding{found(sub.Child("vpc_domain", fmt.Sprint(d.ID)), "vPC 域缺少："+strings.Join(missing, "、"), "configured", missing)}
}

func vpcVTEP(sw *device.Switch, sub assess.Subject) []assess.Finding {
	cfg := sw.Config
	if cfg.NVE == nil || !sw.Role.VTEP() {
		return nil
	}
	lo := cfg.Interfaces[cfg.NVE.SourceInterface]
	anycast := len(lo.Secondary) > 0
	nve := sub.Child("interface", cfg.NVE.Name)
	var out []assess.Finding
	switch {
	case cfg.VPC != nil && !anycast:
		out = append(out, found(nve, "vPC 成员的 NVE 源接口 "+cfg.NVE.SourceInterface+" 没有 secondary（anycast VTEP）地址", "secondary address", "absent"))
	case cfg.VPC == nil && anycast:
		out = append(out, found(nve, "非 vPC 交换机的 NVE 源接口带有 secondary 地址", "absent", "secondary address"))
	}
	if cfg.VPC != nil && anycast {
		if !cfg.NVE.AdvertiseVirtualRMAC {
			out = append(out, found(nve, "vPC anycast VTEP 未配置 advertise virtual-rmac", device.Enabled, ""))
		}
		if cfg.BGP != nil && !cfg.BGP.AdvertisePIP {
			out = append(out, found(sub.Child("bgp", cfg.BGP.ASN.String()), "vPC anycast VTEP 未配置 advertise-pip，单归属主机的 Type-5 路由可能黑洞", device.Enabled, ""))
		}
	}
	if cfg.VPC == nil && cfg.NVE.AdvertiseVirtualRMAC {
		out = append(out, found(nve, "非 vPC 交换机配置了 advertise virtual-rmac", "", device.Enabled))
	}
	return out
}

func virtualPeerLinkTCAM(sw *device.Switch, sub assess.Subject) []assess.Finding {
	cfg := sw.Config
	if cfg.PeerLink() != device.PeerLinkVirtual {
		return nil
	}
	if cfg.TCAMRegions["ing-flow-redirect"] > 0 {
		return nil
	}
	return []assess.Finding{found(sub.Child("tcam", "ing-flow-redirect"), "启用虚拟 peer-link 但未划分 hardware access-list tcam region ing-flow-redirect", "512", "0")}
}

func snmpWeak(sw *device.Switch, sub assess.Subject) []assess.Finding {
	s := sw.Config.Management.SNMP
	var out []assess.Finding
	for _, u := range s.Users {
		if u.Auth == "md5" || u.Priv == "" || u.Priv == "des" {
			out = append(out, found(sub.Child("snmp_user", u.Name), fmt.Sprintf("SNMPv3 用户 %s 使用 auth %s priv %s", u.Name, u.Auth, orNone(u.Priv)), "sha + aes-128", u.Auth+"/"+orNone(u.Priv)))
		}
	}
	for i, c := range s.Communities {
		if c.ACL == "" {
			out = append(out, found(sub.Child("snmp_community", fmt.Sprint(i+1)), "SNMP community 未绑定 use-ipv4acl", "acl", "none"))
		}
	}
	return out
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

func nvePeersDown(sw *device.Switch, sub assess.Subject) []assess.Finding {
	var out []assess.Finding
	for _, p := range sw.State.NVEPeers {
		if !p.Up {
			out = append(out, found(sub.Child("nve_peer", p.Peer), "NVE 对端 "+p.Peer+" 状态 "+p.State, "Up", p.State))
		}
	}
	return out
}

func nveVNIsDown(sw *device.Switch, sub assess.Subject) []assess.Finding {
	var out []assess.Finding
	for _, v := range sw.State.NVEVNIs {
		if !v.Up {
			out = append(out, found(sub.Child("vni", v.VNI.String()), fmt.Sprintf("VNI %s (%s %s) 状态 %s", v.VNI, v.Layer, v.Domain, v.State), "Up", v.State))
		}
	}
	return out
}

func vnisNotOperational(sw *device.Switch, sub assess.Subject) []assess.Finding {
	if sw.Config.NVE == nil {
		return nil
	}
	seen := map[fabric.VNI]bool{}
	for _, v := range sw.State.NVEVNIs {
		seen[v.VNI] = true
	}
	var out []assess.Finding
	for _, vni := range slices.Sorted(maps.Keys(sw.Config.NVE.Members)) {
		if !seen[vni] {
			out = append(out, found(sub.Child("vni", vni.String()), "member vni "+vni.String()+" 未出现在 show nve vni 中", "operational", "absent"))
		}
	}
	return out
}

func bgpDown(sw *device.Switch, sub assess.Subject) []assess.Finding {
	var out []assess.Finding
	for _, n := range sw.State.BGP.Neighbors {
		if !n.Established {
			out = append(out, found(sub.Child("bgp_neighbor", n.Address), "EVPN 邻居 "+n.Address+" 状态 "+n.State, "Established", n.State))
		}
	}
	return out
}

func bgpNoPrefixes(sw *device.Switch, sub assess.Subject) []assess.Finding {
	var out []assess.Finding
	for _, n := range sw.State.BGP.Neighbors {
		if n.Established && n.Prefixes == 0 && sw.Role != fabric.RoleSpine {
			out = append(out, found(sub.Child("bgp_neighbor", n.Address), "EVPN 邻居 "+n.Address+" 已建立但收到 0 条前缀", ">0", 0))
		}
	}
	return out
}

func ospfNotFull(sw *device.Switch, sub assess.Subject) []assess.Finding {
	var out []assess.Finding
	for _, n := range sw.State.OSPF {
		if !n.Full {
			out = append(out, found(sub.Child("ospf_neighbor", n.RouterID), fmt.Sprintf("OSPF %s 邻居 %s（%s）状态 %s", n.Process, n.RouterID, n.Interface, n.State), "FULL", n.State))
		}
	}
	return out
}

func vpcUnhealthy(sw *device.Switch, sub assess.Subject) []assess.Finding {
	v := sw.State.VPC
	if v == nil {
		return nil
	}
	dom := sub.Child("vpc_domain", fmt.Sprint(v.DomainID))
	var out []assess.Finding
	if !v.PeerOK {
		out = append(out, found(dom, "vPC 对端状态："+v.PeerStatus, "peer adjacency formed ok", v.PeerStatus))
	}
	if !v.KeepaliveOK {
		out = append(out, found(dom, "vPC keepalive："+v.KeepaliveStatus, "peer is alive", v.KeepaliveStatus))
	}
	if !v.ConsistencyOK || !v.PerVLANConsistent || !v.Type2Consistent {
		out = append(out, found(dom, fmt.Sprintf("vPC 一致性：全局 %v / per-vlan %v / type-2 %v", v.ConsistencyOK, v.PerVLANConsistent, v.Type2Consistent), "success", "failed"))
	}
	for _, m := range v.Members {
		if !m.Consistent && m.Up {
			out = append(out, found(dom.Child("vpc", fmt.Sprint(m.ID)), fmt.Sprintf("vPC %d (%s) 一致性检查未通过：%s", m.ID, m.Port, m.Reason), "success", m.Reason))
		}
	}
	return out
}

func portChannelDown(sw *device.Switch, sub assess.Subject) []assess.Finding {
	var out []assess.Finding
	for _, pc := range sw.State.PortChannels {
		if !pc.Up {
			continue
		}
		for _, m := range pc.Members {
			if !m.Bundled {
				out = append(out, found(sub.Child("port_channel", pc.Name).Child("member", m.Port), fmt.Sprintf("%s 成员 %s 标志 %s", pc.Name, m.Port, m.Flag), "P", m.Flag))
			}
		}
	}
	return out
}

func duplicateMACs(sw *device.Switch, sub assess.Subject) []assess.Finding {
	var out []assess.Finding
	for _, r := range sw.State.L2Routes {
		if strings.Contains(r.Flags, "Dup") || strings.Contains(r.Flags, "PF") {
			out = append(out, found(sub.Child("mac", string(r.MAC)), fmt.Sprintf("拓扑 %s MAC %s 标志 %s", r.Topology, r.MAC, r.Flags), "unique", r.Flags))
		}
	}
	return out
}
