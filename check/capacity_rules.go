package check

import (
	"fmt"

	"github.com/gatework/go-netops/assess"
	"github.com/gatework/go-netops/evidence"

	"github.com/gatework/fabric/capacity"
	"github.com/gatework/fabric/device"
)

// Capacity rule identifiers.
const (
	RulePoolUtilization assess.RuleID = "fabric.pool_utilization"
	RuleSwitchScale     assess.RuleID = "fabric.switch_scale"
)

func capacityParams() assess.Params {
	d := capacity.DefaultLimits()
	return assess.Params{"percent": 80, "vlans": d.VLANs, "macs": d.MACs, "nve_peers": d.NVEPeers, "port_channels": d.PortChannels}
}

func limitsOf(p assess.Params) capacity.Limits {
	d := capacity.DefaultLimits()
	return capacity.Limits{
		VLANs: p.Int("vlans", d.VLANs), MACs: p.Int("macs", d.MACs),
		NVEPeers: p.Int("nve_peers", d.NVEPeers), PortChannels: p.Int("port_channels", d.PortChannels),
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
	meta := assess.RuleMeta{ID: RulePoolUtilization, Title: "资源池利用率过高", Severity: assess.SeverityMedium,
		Category: assess.CategoryCapacity, Requires: []evidence.Dimension{device.DimConfig}, Params: capacityParams()}
	return assess.NewRule(meta, func(in assess.Input[*Fabric]) []assess.Finding {
		f := in.Model
		if f.Intent == nil {
			return nil
		}
		percent := in.Params.Float("percent", 80)
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
	meta := assess.RuleMeta{ID: RuleSwitchScale, Title: "单机规模接近上限", Severity: assess.SeverityMedium,
		Category: assess.CategoryCapacity, Requires: []evidence.Dimension{device.DimConfig}, Params: capacityParams()}
	return assess.NewRule(meta, func(in assess.Input[*device.Switch]) []assess.Finding {
		sw := in.Model
		percent := in.Params.Float("percent", 80)
		var out []assess.Finding
		for _, u := range capacity.Compute("", nil, []*device.Switch{sw}, limitsOf(in.Params)).Switches[0].All() {
			if over(u, percent) {
				out = append(out, found(switchSubject(sw).Child("scale", u.Name), usageText(u), percent, u.Percent()))
			}
		}
		return out
	})
}
