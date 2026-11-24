package workflow

import (
	"cmp"
	"context"
	"fmt"
	"slices"

	"github.com/gatework/netops/assess"

	"github.com/gatework/fabric"
	"github.com/gatework/fabric/capacity"
	"github.com/gatework/fabric/check"
	"github.com/gatework/fabric/device"
	"github.com/gatework/fabric/endpoint"
	"github.com/gatework/fabric/maintenance"
	"github.com/gatework/fabric/status"
	"github.com/gatework/fabric/topology"
)

// Status runs the assessment and summarizes health per switch and region.
func Status(ctx context.Context, req CheckRequest) (status.Report, check.Report, error) {
	rep, err := Check(ctx, req)
	if err != nil {
		return status.Report{}, rep, err
	}
	var inputs []status.Input
	for _, name := range req.Fleet.Regions() {
		if req.Region != "" && name != req.Region {
			continue
		}
		in := status.Input{Name: name, Switches: req.Fleet.Members(name), Findings: regionFindings(rep, req.Fleet, name)}
		if req.Fleet.Intent != nil {
			in.Intent, _ = req.Fleet.Intent.Region(name)
		}
		inputs = append(inputs, in)
	}
	return status.Build(inputs), rep, nil
}

func regionFindings(rep check.Report, f *Fleet, region string) []assess.Finding {
	out := append([]assess.Finding(nil), rep.Fabrics[region].Findings...)
	for _, sw := range f.Members(region) {
		out = append(out, rep.Switches[sw.Key()].Findings...)
	}
	return out
}

// Endpoints indexes hosts across every region of the fleet.
func Endpoints(f *Fleet) *endpoint.Index {
	var regions []endpoint.Region
	for _, name := range f.Regions() {
		r := endpoint.Region{Name: name, Switches: f.Members(name), Peers: map[string]string{}}
		if f.Intent != nil {
			if ir, ok := f.Intent.Region(name); ok {
				for _, p := range ir.VPCPairs {
					r.Peers[p.Members[0].Switch], r.Peers[p.Members[1].Switch] = p.Members[1].Switch, p.Members[0].Switch
				}
			}
		}
		regions = append(regions, r)
	}
	return endpoint.Build(regions)
}

// Topology audits the cabling of every intent region.
func Topology(f *Fleet, region string) []topology.Audit {
	var out []topology.Audit
	if f.Intent == nil {
		return nil
	}
	for _, r := range f.Intent.Regions {
		if region == "" || r.Name == region {
			out = append(out, topology.Run(r, f.Members(r.Name)))
		}
	}
	return out
}

// Capacity measures every region of the fleet.
func Capacity(f *Fleet, region string, lim capacity.Limits) []capacity.Region {
	var out []capacity.Region
	for _, name := range f.Regions() {
		if region != "" && name != region {
			continue
		}
		var ir *fabric.Region
		if f.Intent != nil {
			ir, _ = f.Intent.Region(name)
		}
		out = append(out, capacity.Compute(name, ir, f.Members(name), lim))
	}
	return out
}

// SwitchDetail is everything known about one switch: its health, its
// fabric uplinks, its control-plane sessions and its findings.
type SwitchDetail struct {
	Switch  *device.Switch       `json:"-"`
	Health  status.Switch        `json:"health"`
	Links   []topology.LinkCheck `json:"links"`
	Uplinks *topology.Uplinks    `json:"uplinks,omitempty"`
	// Downlinks is set for a spine; AtRisk lists the VTEPs that lost
	// redundancy towards it.
	Downlinks *topology.SpineLoad `json:"downlinks,omitempty"`
	AtRisk    []topology.Uplinks  `json:"at_risk,omitempty"`
	// Topology is the region's audit the switch belongs to.
	Topology topology.Audit `json:"-"`
	// Findings are the switch's own findings and the region findings
	// that name it, most severe first.
	Findings []assess.Finding `json:"findings"`
	// Peers names the switches by router ID and VTEP address, so that
	// sessions read as names.
	Peers map[string]string `json:"-"`
}

// Inspect builds the detail of one switch.
func Inspect(ctx context.Context, req CheckRequest, name string) (SwitchDetail, error) {
	sw, err := req.Fleet.Switch(name)
	if err != nil {
		return SwitchDetail{}, err
	}
	req.Region = sw.Region
	health, rep, err := Status(ctx, req)
	if err != nil {
		return SwitchDetail{}, err
	}
	d := SwitchDetail{Switch: sw, Peers: peerNames(req.Fleet, sw.Region)}
	for _, r := range health.Regions {
		if i := slices.IndexFunc(r.Switches, func(s status.Switch) bool { return s.Name == sw.Name }); i >= 0 {
			d.Health = r.Switches[i]
		}
	}
	for _, f := range regionFindings(rep, req.Fleet, sw.Region) {
		if Names(f.Subject, sw.Name) {
			d.Findings = append(d.Findings, f)
		}
	}
	slices.SortStableFunc(d.Findings, func(a, b assess.Finding) int { return cmp.Compare(b.Severity, a.Severity) })
	for _, a := range Topology(req.Fleet, sw.Region) {
		d.Topology = a
		for _, c := range a.Links {
			if c.A.Switch == sw.Name || c.B.Switch == sw.Name {
				d.Links = append(d.Links, c)
			}
		}
		if i := slices.IndexFunc(a.Uplinks, func(u topology.Uplinks) bool { return u.Switch == sw.Name }); i >= 0 {
			d.Uplinks = &a.Uplinks[i]
		}
		if i := slices.IndexFunc(a.SpineLoads, func(s topology.SpineLoad) bool { return s.Switch == sw.Name }); i >= 0 {
			d.Downlinks = &a.SpineLoads[i]
			for _, u := range a.AtRisk() {
				if slices.Contains(u.Lost, sw.Name) {
					d.AtRisk = append(d.AtRisk, u)
				}
			}
		}
	}
	return d, nil
}

// peerNames maps the region's router IDs and VTEP addresses to switch
// names.
func peerNames(f *Fleet, region string) map[string]string {
	out := map[string]string{}
	if f.Intent != nil {
		if r, ok := f.Intent.Region(region); ok {
			for _, s := range r.Switches {
				if s.RouterID.IsValid() {
					out[s.RouterID.String()] = s.Name
				}
				if s.VTEP.IsValid() {
					out[s.VTEP.String()] = s.Name
				}
			}
		}
	}
	for _, peer := range f.Members(region) {
		if peer.Config == nil {
			continue
		}
		if a, ok := peer.Config.VTEPAddress(); ok {
			out[a.String()] = peer.Name
		}
	}
	return out
}

// Names reports whether a finding subject names the switch.
func Names(s assess.Subject, name string) bool {
	for _, p := range s.Parts() {
		if p.Kind == "switch" && p.Key == name {
			return true
		}
	}
	return false
}

// Maintenance assesses what breaks while one switch is out of service.
func Maintenance(f *Fleet, name string) (maintenance.Report, error) {
	sw, err := f.Switch(name)
	if err != nil {
		return maintenance.Report{}, err
	}
	if f.Intent == nil {
		return maintenance.Report{}, fmt.Errorf("workflow: maintenance needs the intent")
	}
	r, ok := f.Intent.Region(sw.Region)
	if !ok {
		return maintenance.Report{}, fmt.Errorf("workflow: no region %q", sw.Region)
	}
	audits := Topology(f, sw.Region)
	if len(audits) == 0 {
		return maintenance.Report{}, fmt.Errorf("workflow: no topology for %q", sw.Region)
	}
	return maintenance.Assess(r, audits[0], f.Members(sw.Region), name)
}
