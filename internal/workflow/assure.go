package workflow

import (
	"context"

	"github.com/gatework/go-netops/assess"

	"github.com/gatework/fabric"
	"github.com/gatework/fabric/capacity"
	"github.com/gatework/fabric/check"
	"github.com/gatework/fabric/endpoint"
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
