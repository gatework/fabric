package check

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"sync"

	"github.com/gatework/netops/assess"
	"github.com/gatework/netops/evidence"

	"github.com/gatework/fabric"
	"github.com/gatework/fabric/baseline"
	"github.com/gatework/fabric/device"
	"github.com/gatework/fabric/topology"
)

// Fabric is the evidence of one region, with the expectations it is
// checked against.
type Fabric struct {
	Name     string
	Switches []*device.Switch
	// Intent is the region's declared state; nil skips intent drift.
	Intent *fabric.Region
	// Baseline is the golden configuration; nil skips baseline drift.
	Baseline *baseline.Profile
	// Previous holds the last collection by switch key; nil skips
	// regression rules.
	Previous map[string]*device.Switch

	auditOnce sync.Once
	audit     topology.Audit
}

// Topology returns the cabling audit, computed once.
func (f *Fabric) Topology() topology.Audit {
	f.auditOnce.Do(func() {
		r := f.Intent
		if r == nil {
			r = &fabric.Region{Name: f.Name}
		}
		f.audit = topology.Run(r, f.Switches)
	})
	return f.audit
}

// Coverage is complete for a dimension only when every switch observed
// it completely.
func (f *Fabric) Coverage() evidence.Coverage {
	out := evidence.Coverage{}
	for _, d := range device.Dimensions() {
		status := evidence.StatusComplete
		for _, sw := range f.Switches {
			if s := sw.Coverage.Status(d); !s.Complete() {
				status = s
				break
			}
		}
		if len(f.Switches) == 0 {
			status = evidence.StatusNotRequested
		}
		out = out.With(d, status)
	}
	return out
}

// VTEPs returns the leaf and border switches.
func (f *Fabric) VTEPs() []*device.Switch {
	var out []*device.Switch
	for _, sw := range f.Switches {
		if sw.Role.VTEP() {
			out = append(out, sw)
		}
	}
	return out
}

// ByRole returns the switches with role.
func (f *Fabric) ByRole(role fabric.Role) []*device.Switch {
	var out []*device.Switch
	for _, sw := range f.Switches {
		if sw.Role == role {
			out = append(out, sw)
		}
	}
	return out
}

func switchSubject(sw *device.Switch) assess.Subject {
	s := assess.NewSubject("switch", sw.Name)
	if sw.Region != "" {
		s = assess.NewSubject("region", sw.Region).Child("switch", sw.Name)
	}
	return s
}

func fabricSubject(f *Fabric) assess.Subject { return assess.NewSubject("region", f.Name) }

// Report is the assessment of every fabric and switch.
type Report struct {
	Fabrics  map[string]assess.Assessment `json:"fabrics"`
	Switches map[string]assess.Assessment `json:"switches"`
}

// Findings returns every finding, fabric findings first, in name order.
func (r Report) Findings() []assess.Finding {
	var out []assess.Finding
	for _, name := range slices.Sorted(maps.Keys(r.Fabrics)) {
		out = append(out, r.Fabrics[name].Findings...)
	}
	for _, name := range slices.Sorted(maps.Keys(r.Switches)) {
		out = append(out, r.Switches[name].Findings...)
	}
	return out
}

// Incomplete reports whether any enabled rule was skipped or failed.
func (r Report) Incomplete() bool {
	for _, a := range r.Fabrics {
		if a.Incomplete() {
			return true
		}
	}
	for _, a := range r.Switches {
		if a.Incomplete() {
			return true
		}
	}
	return false
}

// Assessor runs both catalogs under one policy.
type Assessor struct {
	switches *assess.Engine[*device.Switch]
	fabrics  *assess.Engine[*Fabric]
}

// NewAssessor builds the catalogs and validates policy against them.
// Rule IDs in policy that belong to neither catalog are rejected.
func NewAssessor(policy assess.Policy, verify assess.VerifyCommands) (*Assessor, error) {
	sc, err := assess.NewCatalog(SwitchRules()...)
	if err != nil {
		return nil, fmt.Errorf("check: %w", err)
	}
	fc, err := assess.NewCatalog(FabricRules()...)
	if err != nil {
		return nil, fmt.Errorf("check: %w", err)
	}
	if err := policy.Check(sc.IDs(), fc.IDs()); err != nil {
		return nil, fmt.Errorf("check: %w", err)
	}
	o := assess.Options{Verify: verify, Partial: true}
	se, err := assess.NewEngine(sc, policy, o)
	if err != nil {
		return nil, fmt.Errorf("check: %w", err)
	}
	fe, err := assess.NewEngine(fc, policy, o)
	if err != nil {
		return nil, fmt.Errorf("check: %w", err)
	}
	return &Assessor{switches: se, fabrics: fe}, nil
}

// Run assesses every fabric and its switches.
func (a *Assessor) Run(ctx context.Context, fabrics []*Fabric) (Report, error) {
	rep := Report{Fabrics: map[string]assess.Assessment{}, Switches: map[string]assess.Assessment{}}
	for _, f := range fabrics {
		res, err := a.fabrics.Run(ctx, f, f.Coverage())
		rep.Fabrics[f.Name] = res
		if err != nil {
			return rep, fmt.Errorf("check: fabric %s: %w", f.Name, err)
		}
		for _, sw := range f.Switches {
			res, err := a.switches.Run(ctx, sw, sw.Coverage)
			rep.Switches[sw.Key()] = res
			if err != nil {
				return rep, fmt.Errorf("check: switch %s: %w", sw.Name, err)
			}
		}
	}
	return rep, nil
}

// Fabric assesses one fabric with the fabric catalog. It is safe to call
// concurrently.
func (a *Assessor) Fabric(ctx context.Context, f *Fabric) (assess.Assessment, error) {
	return a.fabrics.Run(ctx, f, f.Coverage())
}

// Switch assesses one switch with the switch catalog. It is safe to call
// concurrently.
func (a *Assessor) Switch(ctx context.Context, sw *device.Switch) (assess.Assessment, error) {
	return a.switches.Run(ctx, sw, sw.Coverage)
}

// RuleIDs lists every rule identifier, switch rules first.
func RuleIDs() []assess.RuleID {
	var out []assess.RuleID
	for _, r := range SwitchRules() {
		out = append(out, r.Meta().ID)
	}
	for _, r := range FabricRules() {
		out = append(out, r.Meta().ID)
	}
	return out
}
