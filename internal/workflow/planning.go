package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/gatework/netops/change"
	ndevice "github.com/gatework/netops/device"
	"github.com/gatework/netops/evidence"

	"github.com/gatework/fabric"
	"github.com/gatework/fabric/baseline"
	"github.com/gatework/fabric/device"
	"github.com/gatework/fabric/governance"
	"github.com/gatework/fabric/internal/parallel"
	"github.com/gatework/fabric/plan"
	"github.com/gatework/fabric/rollout"
	"github.com/gatework/fabric/vendors/nxos"
)

// errUnchanged marks a switch already in its desired state.
var errUnchanged = errors.New("workflow: switch unchanged")

// ErrNoIntent reports planning without an intent.
var ErrNoIntent = errors.New("workflow: planning needs an intent")

// PlanRequest selects what to plan.
type PlanRequest struct {
	Fleet *Fleet
	// Baseline adds golden-configuration remediation; nil plans intent only.
	Baseline *baseline.Profile
	// Region and Switches narrow the plan; empty means everything.
	Region   string
	Switches []string
	Prune    bool
	// Reset and Remove carry a provisioning result's targeted deletions:
	// ports to return to factory state and retired networks or BGP
	// neighbors, per switch.
	Reset    map[string][]string
	Networks map[fabric.VLAN]fabric.VNI
	Removed  map[string][]string
	Strategy rollout.Strategy
	Now      time.Time
	// Before, when set, scopes the plan to the change from Before to the
	// fleet's intent: drift that both intents would correct is left out.
	Before *fabric.Intent
	// Change is the change record bound to the bundle.
	Change governance.Record
	// Risk tunes classification; the zero value uses the defaults.
	Risk governance.Policy
}

// Item is the plan for one switch.
type Item struct {
	Member rollout.Member               `json:"member"`
	Mgmt   string                       `json:"mgmt"`
	Plan   *change.Plan[*device.Config] `json:"-"`
	Ops    []plan.Op                    `json:"-"`
	// Review is the indented configuration an operator reads.
	Review string `json:"-"`
	// Summary counts operations by "action kind".
	Summary map[string]int `json:"summary"`
	Impact  plan.Impact    `json:"impact"`
}

// Refusal is a switch that could not be planned.
type Refusal struct {
	Switch string `json:"switch"`
	Reason string `json:"reason"`
}

// Bundle is a reviewed, executable change across switches.
type Bundle struct {
	ID        string            `json:"id"`
	CreatedAt time.Time         `json:"created_at"`
	Items     []*Item           `json:"items"`
	Waves     []rollout.Wave    `json:"waves"`
	Refused   []Refusal         `json:"refused"`
	Change    governance.Record `json:"change"`
	Risk      governance.Risk   `json:"risk"`
	// Unchanged counts switches already in the desired state.
	Unchanged int `json:"unchanged"`
}

// Item returns the plan of a switch.
func (b *Bundle) Item(name string) (*Item, bool) {
	for _, it := range b.Items {
		if it.Member.Name == name {
			return it, true
		}
	}
	return nil, false
}

// Plan compiles intent (and baseline) into verified per-switch plans and
// schedules them into waves. Switches whose evidence is incomplete, whose
// plan does not converge or whose result would be unsafe are refused.
func Plan(req PlanRequest) (*Bundle, error) {
	if req.Fleet.Intent == nil {
		return nil, ErrNoIntent
	}
	if req.Now.IsZero() {
		req.Now = time.Now()
	}
	b := &Bundle{CreatedAt: req.Now.UTC(), Refused: []Refusal{}}
	var chosen []*device.Switch
	for _, sw := range req.Fleet.Switches {
		if selected(req, sw) {
			chosen = append(chosen, sw)
		}
	}
	// Switches plan independently; results keep fleet order.
	items, errs := parallel.Map(context.Background(), 0, chosen, func(_ context.Context, sw *device.Switch) (*Item, error) {
		return planSwitch(req, sw)
	})
	for i, sw := range chosen {
		it, err := items[i], errs[i]
		switch {
		case errors.Is(err, errUnchanged):
			b.Unchanged++
		case err != nil:
			b.Refused = append(b.Refused, Refusal{Switch: sw.Name, Reason: err.Error()})
		default:
			b.Items = append(b.Items, it)
		}
	}
	countEndpoints(req.Fleet, b.Items)
	members := make([]rollout.Member, 0, len(b.Items))
	for _, it := range b.Items {
		members = append(members, it.Member)
	}
	b.Change = req.Change
	b.Risk = governance.Assess(riskItems(b.Items), riskPolicy(req.Risk))
	strategy := req.Strategy
	if b.Risk.Class == governance.ClassMajor {
		// Major changes go one switch at a time behind a canary.
		strategy.WaveSize, strategy.NoCanary = 1, false
	}
	b.Waves = rollout.Schedule(members, strategy)
	if err := rollout.Validate(b.Waves); err != nil {
		return nil, err
	}
	id, err := bundleID(b)
	if err != nil {
		return nil, err
	}
	b.ID = id
	return b, nil
}

func selected(req PlanRequest, sw *device.Switch) bool {
	if sw.Region == Unassigned {
		return false
	}
	if req.Region != "" && sw.Region != req.Region {
		return false
	}
	return len(req.Switches) == 0 || slices.Contains(req.Switches, sw.Name)
}

// planSwitch returns errUnchanged when the switch needs no change.
func planSwitch(req PlanRequest, sw *device.Switch) (*Item, error) {
	if !sw.Configured() {
		return nil, fmt.Errorf("configuration evidence is %s", sw.Coverage.Status(device.DimConfig))
	}
	r, s, ok := req.Fleet.Intent.Locate(sw.Name)
	if !ok {
		return nil, fmt.Errorf("switch is not in the intent")
	}
	d := plan.Compile(r, s)
	d.Reset = append(d.Reset, req.Reset[s.Name]...)
	maps.Copy(d.Remove.Networks, req.Networks)
	d.Remove.Neighbors = append(d.Remove.Neighbors, req.Removed[s.Name]...)
	if req.Baseline != nil {
		d.Merge(req.Baseline.Resolve(r.Name, s.Role).Desired(sw.Config))
	}
	opt := plan.Options{Prune: req.Prune}
	ops, err := operations(req, sw, d, opt)
	if err != nil {
		return nil, err
	}
	if len(ops) == 0 {
		return nil, errUnchanged
	}
	if err := plan.Safe(sw.Config, ops); err != nil {
		return nil, err
	}
	return newItem(r, s, sw, ops, req.Now)
}

// operations plans the full desired state, or only the change from
// req.Before when it is set.
func operations(req PlanRequest, sw *device.Switch, d *plan.Desired, opt plan.Options) ([]plan.Op, error) {
	if req.Before == nil {
		ops := plan.Diff(d, sw.Config, opt)
		if len(ops) == 0 {
			return nil, nil
		}
		return ops, plan.Converges(d, sw.Config, ops, opt)
	}
	before := plan.NewDesired()
	if r, s, ok := req.Before.Locate(sw.Name); ok {
		before = plan.Compile(r, s)
		if req.Baseline != nil {
			before.Merge(req.Baseline.Resolve(r.Name, s.Role).Desired(sw.Config))
		}
	}
	return plan.Delta(before, d, sw.Config, opt)
}

func newItem(r *fabric.Region, s *fabric.Switch, sw *device.Switch, ops []plan.Op, at time.Time) (*Item, error) {
	cs, err := plan.NewChangeset(ops, nxos.Renderer{})
	if err != nil {
		return nil, err
	}
	meta := change.Meta{ID: "fabric-" + safe(sw.Name), Created: at.UTC()}
	target := ndevice.Device{ID: sw.Mgmt.String(), Name: sw.Name, Mgmt: sw.Mgmt, Platform: nxos.Platform}
	p, err := plan.NewPlan(meta, target, sw.Config, cs)
	if err != nil {
		return nil, fmt.Errorf("workflow: %w", err)
	}
	review, err := nxos.RenderConfig(cs.Ops())
	if err != nil {
		return nil, err
	}
	summary := map[string]int{}
	for _, op := range ops {
		summary[op.Action().String()+" "+string(op.Ref().Kind)]++
	}
	peer, _ := r.Peer(s.Name)
	return &Item{
		Member:  rollout.Member{Name: s.Name, Region: r.Name, Role: s.Role, VPCPeer: peer},
		Mgmt:    sw.Mgmt.String(),
		Plan:    p,
		Ops:     cs.Ops(),
		Review:  review,
		Summary: summary,
		Impact:  plan.ImpactOf(cs.Ops(), sw.Config),
	}, nil
}

// bundleID is a digest of every envelope, so editing any plan changes it.
func bundleID(b *Bundle) (string, error) {
	var envs []change.Envelope
	for _, it := range b.Items {
		env, err := it.Plan.Envelope()
		if err != nil {
			return "", fmt.Errorf("workflow: %w", err)
		}
		envs = append(envs, env)
	}
	body, err := json.Marshal(struct {
		Plans  []change.Envelope `json:"plans"`
		Change governance.Record `json:"change"`
		Risk   governance.Risk   `json:"risk"`
	}{envs, b.Change, b.Risk})
	if err != nil {
		return "", fmt.Errorf("workflow: %w", err)
	}
	return string(evidence.DigestOf(body))[:12], nil
}

func riskPolicy(p governance.Policy) governance.Policy {
	if p == (governance.Policy{}) {
		return governance.DefaultPolicy()
	}
	return p
}

func riskItems(items []*Item) []governance.Item {
	out := make([]governance.Item, 0, len(items))
	for _, it := range items {
		out = append(out, governance.Item{Switch: it.Member.Name, Region: it.Member.Region, Role: it.Member.Role, Summary: it.Summary, Impact: it.Impact})
	}
	return out
}

// countEndpoints fills each item's endpoint count: hosts learned locally
// on the VLANs and ports whose existing state the item changes. It needs
// MAC evidence; without it the count stays zero.
func countEndpoints(f *Fleet, items []*Item) {
	if len(items) == 0 {
		return
	}
	idx := Endpoints(f)
	for _, it := range items {
		n := 0
		for _, d := range it.Impact.Disruptive {
			kind, key, _ := strings.Cut(d, ":")
			switch kind {
			case "vlan":
				if v, err := fabric.ParseVLAN(key); err == nil {
					n += idx.AttachedOn(it.Member.Name, v, "")
				}
			case "port":
				n += idx.AttachedOn(it.Member.Name, 0, key)
			}
		}
		it.Impact.Endpoints = n
	}
}
