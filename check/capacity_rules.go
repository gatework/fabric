package check

import (
	"fmt"

	"github.com/gatework/netops/assess"
	"github.com/gatework/netops/evidence"

	"github.com/gatework/fabric/capacity"
	"github.com/gatework/fabric/device"
)

// Capacity rule identifiers.
const (
	RulePoolUtilization assess.RuleID = "fabric.pool_utilization"
	RuleSwitchScale     assess.RuleID = "fabric.switch_scale"
)

func capacityParams() []assess.Param {
	d := capacity.DefaultLimits()
	return []assess.Param{
		assess.Number("percent", 80), assess.Integer("vlans", d.VLANs), assess.Integer("macs", d.MACs),
		assess.Integer("nve_peers", d.NVEPeers), assess.Integer("port_channels", d.PortChannels),
	}
}

func limitsOf(p assess.Params) capacity.Limits {
	return capacity.Limits{
		VLANs: p.Integer("vlans"), MACs: p.Integer("macs"),
		NVEPeers: p.Integer("nve_peers"), PortChannels: p.Integer("port_channels"),
	}
}

func over(u capacity.Usage, percent float64) bool {
	return u.Total > 0 && u.Used >= 0 && u.Percent() >= percent
}

func usageText(u capacity.Usage) string {
	text := fmt.Sprintf("%s 已用 %d/%d（%.1f%%）", u.Name, u.Used, u.Total, u.Percent())
	if u.Percent() >= 95 {
		text += "，即将耗尽"
	}
	return text
}

func poolRule() assess.Rule[*Fabric] {
	meta := assess.Meta{ID: RulePoolUtilization, Title: "资源池利用率过高", Severity: assess.SeverityMedium,
		Category: assess.CategoryCapacity, Requires: []evidence.Dimension{device.DimConfig}, Params: capacityParams()}
	return assess.Check(meta, func(in assess.Input[*Fabric]) []assess.Finding {
		f := in.Model
		if f.Intent == nil {
			return nil
		}
		percent := in.Params.Number("percent")
		var out []assess.Finding
		for _, u := range capacity.Compute(f.Name, f.Intent, f.Switches, limitsOf(in.Params)).Pools {
			if over(u, percent) {
				out = append(out, found(fabricSubject(f).Child("pool", u.Name), usageText(u), percent, u.Percent()))
			}
		}
		return out
	})
}

func scaleRule() assess.Rule[*device.Switch] {
	meta := assess.Meta{ID: RuleSwitchScale, Title: "单机规模接近上限", Severity: assess.SeverityMedium,
		Category: assess.CategoryCapacity, Requires: []evidence.Dimension{device.DimConfig}, Params: capacityParams()}
	return assess.Check(meta, func(in assess.Input[*device.Switch]) []assess.Finding {
		sw := in.Model
		percent := in.Params.Number("percent")
		var out []assess.Finding
		c := capacity.Compute("", nil, []*device.Switch{sw}, limitsOf(in.Params)).Switches[0]
		for _, u := range c.All() {
			if !over(u, percent) {
				continue
			}
			text := usageText(u)
			if u == c.Ports && sw.Has(device.DimInterfaces) {
				text += fmt.Sprintf("：up %d，down 但有描述/路由/聚合的预留口 %d（可核对回收）", c.PortsUp, c.PortsReserved)
			}
			out = append(out, found(switchSubject(sw).Child("scale", u.Name), text, percent, u.Percent()))
		}
		return out
	})
}
