package workflow

import (
	"context"
	"errors"
	"fmt"

	"github.com/gatework/go-netops/assess"

	"github.com/gatework/fabric"
	"github.com/gatework/fabric/adopt"
	"github.com/gatework/fabric/baseline"
	"github.com/gatework/fabric/check"
	"github.com/gatework/fabric/device"
	"github.com/gatework/fabric/internal/parallel"
	"github.com/gatework/fabric/vendors/nxos"
)

// CheckRequest selects what a check compares against.
type CheckRequest struct {
	Fleet    *Fleet
	Baseline *baseline.Profile
	Previous *Fleet
	Policy   assess.Policy
	Region   string
}

// Check assesses every region of the fleet.
func Check(ctx context.Context, req CheckRequest) (check.Report, error) {
	a, err := check.NewAssessor(req.Policy, nxos.Profile{})
	if err != nil {
		return check.Report{}, err
	}
	var fabrics []*check.Fabric
	for _, name := range req.Fleet.Regions() {
		if req.Region != "" && name != req.Region {
			continue
		}
		f := &check.Fabric{Name: name, Switches: req.Fleet.Members(name), Baseline: req.Baseline}
		if req.Fleet.Intent != nil {
			f.Intent, _ = req.Fleet.Intent.Region(name)
		}
		if req.Previous != nil {
			f.Previous = req.Previous.ByKey()
		}
		fabrics = append(fabrics, f)
	}
	if len(fabrics) == 0 {
		return check.Report{}, fmt.Errorf("workflow: no region %q", req.Region)
	}
	return assessAll(ctx, a, fabrics)
}

// assessAll runs fabric and switch catalogs concurrently; rules are pure,
// so the report equals a sequential run.
func assessAll(ctx context.Context, a *check.Assessor, fabrics []*check.Fabric) (check.Report, error) {
	rep := check.Report{Fabrics: map[string]assess.Assessment{}, Switches: map[string]assess.Assessment{}}
	fres, ferrs := parallel.Map(ctx, 0, fabrics, a.Fabric)
	var switches []*device.Switch
	for i, f := range fabrics {
		rep.Fabrics[f.Name] = fres[i]
		switches = append(switches, f.Switches...)
	}
	sres, serrs := parallel.Map(ctx, 0, switches, a.Switch)
	for i, sw := range switches {
		rep.Switches[sw.Key()] = sres[i]
	}
	return rep, errors.Join(append(ferrs, serrs...)...)
}

// Import drafts an intent from the fleet, one region per fleet region.
func Import(f *Fleet) (*fabric.Intent, []adopt.Conflict) {
	in := &fabric.Intent{Schema: fabric.IntentSchema}
	conflicts := []adopt.Conflict{}
	for _, name := range f.Regions() {
		rep := adopt.Region(name, f.Members(name))
		in.Regions = append(in.Regions, rep.Region)
		conflicts = append(conflicts, rep.Conflicts...)
	}
	return in, conflicts
}

// DeriveBaseline drafts a baseline from the fleet's majority.
func DeriveBaseline(f *Fleet) *baseline.Profile {
	var groups []baseline.Group
	for _, name := range f.Regions() {
		groups = append(groups, baseline.Group{Region: name, Switches: f.Members(name)})
	}
	return baseline.Derive(groups)
}
