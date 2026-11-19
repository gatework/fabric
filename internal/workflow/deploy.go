package workflow

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"sync"
	"time"

	"github.com/gatework/go-netops/apply"
	"github.com/gatework/go-netops/change"
	"github.com/gatework/go-netops/evidence"
	"github.com/gatework/go-netops/session"

	"github.com/gatework/fabric/device"
	"github.com/gatework/fabric/governance"
	"github.com/gatework/fabric/rollout"
	"github.com/gatework/fabric/vendors/nxos"
)

// Switch is what deployment needs from one device. Every method opens and
// closes its own session.
type Switch interface {
	// Run executes read-only commands and returns outputs by command.
	Run(ctx context.Context, commands []string) (map[string]string, error)
	// Checkpoint reads the running configuration, then records it under
	// name, in one session, and returns what it read.
	Checkpoint(ctx context.Context, name string) (*device.Config, error)
	// Rollback restores a checkpoint and returns the configuration after.
	Rollback(ctx context.Context, name string) (*device.Config, error)
	// Save copies the running configuration to startup.
	Save(ctx context.Context) error
	// Opener lends a session to go-netops apply.
	Opener() apply.Opener[*device.Config]
}

// Dialer returns the device behind a management address.
type Dialer func(mgmt netip.Addr) Switch

// Outcome is how one switch's deployment ended.
type Outcome string

// Outcomes. Only OutcomeVerified lets the rollout continue.
const (
	OutcomeVerified  Outcome = "verified"
	OutcomeSkipped   Outcome = "skipped"
	OutcomeDrifted   Outcome = "drifted"
	OutcomeFailed    Outcome = "failed"
	OutcomeUncertain Outcome = "uncertain"
	OutcomeDegraded  Outcome = "degraded"
)

// SwitchResult is one switch's deployment record.
type SwitchResult struct {
	Switch     string          `json:"switch"`
	Started    time.Time       `json:"started"`
	Ended      time.Time       `json:"ended"`
	Before     *rollout.Health `json:"health_before,omitempty"`
	After      *rollout.Health `json:"health_after,omitempty"`
	Mgmt       string          `json:"mgmt"`
	Wave       int             `json:"wave"`
	Outcome    Outcome         `json:"outcome"`
	Checkpoint string          `json:"checkpoint,omitempty"`
	Lost       []string        `json:"lost,omitempty"`
	Saved      bool            `json:"saved,omitempty"`
	Detail     string          `json:"detail,omitempty"`
	State      *apply.State    `json:"state,omitempty"`
}

// DeployReport is the record of a deployment.
type DeployReport struct {
	Bundle   string              `json:"bundle"`
	Change   governance.Record   `json:"change"`
	Risk     governance.Risk     `json:"risk"`
	Decision governance.Decision `json:"decision"`
	Started  time.Time           `json:"started"`
	Ended    time.Time           `json:"ended"`
	Results  []SwitchResult      `json:"results"`
	// StoppedAt is the wave whose failure stopped the rollout, or zero.
	StoppedAt int `json:"stopped_at,omitempty"`
}

// OK reports whether the gate passed and every switch was verified.
func (r DeployReport) OK() bool {
	if !r.Decision.Allowed {
		return false
	}
	for _, s := range r.Results {
		if s.Outcome != OutcomeVerified {
			return false
		}
	}
	return r.StoppedAt == 0
}

// DeployRequest executes a bundle.
type DeployRequest struct {
	Bundle  *Bundle
	Dial    Dialer
	Journal apply.Journal
	Save    bool
	// Gate holds the operator's declarations for the change gate.
	Gate   governance.GateOptions
	Now    func() time.Time
	Logger *slog.Logger
}

// Deploy executes the bundle wave by wave. Switches of a wave run
// concurrently; after a wave with any switch not verified, every later
// wave is skipped. Nothing is retried or rolled back automatically.
//
// The change gate runs first; a refused gate contacts no device.
func Deploy(ctx context.Context, req DeployRequest) (rep DeployReport) {
	if req.Now == nil {
		req.Now = time.Now
	}
	rep = DeployReport{Bundle: req.Bundle.ID, Change: req.Bundle.Change, Risk: req.Bundle.Risk, Started: req.Now()}
	defer func() { rep.Ended = req.Now() }()
	gate := req.Gate
	gate.Now = req.Now()
	rep.Decision, _ = governance.Gate(req.Bundle.Change, req.Bundle.Risk, gate)
	if !rep.Decision.Allowed {
		return rep
	}
	if req.Logger == nil {
		req.Logger = slog.New(slog.DiscardHandler)
	}
	for _, w := range req.Bundle.Waves {
		if rep.StoppedAt != 0 || ctx.Err() != nil {
			for _, m := range w.Switches {
				rep.Results = append(rep.Results, SwitchResult{Switch: m.Name, Wave: w.Index, Outcome: OutcomeSkipped})
			}
			continue
		}
		results := runWave(ctx, req, w)
		rep.Results = append(rep.Results, results...)
		for _, r := range results {
			if r.Outcome != OutcomeVerified {
				rep.StoppedAt = w.Index
			}
		}
	}
	return rep
}

func runWave(ctx context.Context, req DeployRequest, w rollout.Wave) []SwitchResult {
	out := make([]SwitchResult, len(w.Switches))
	var wg sync.WaitGroup
	for i, m := range w.Switches {
		wg.Go(func() {
			it, ok := req.Bundle.Item(m.Name)
			if !ok {
				out[i] = SwitchResult{Switch: m.Name, Wave: w.Index, Outcome: OutcomeFailed, Detail: "no plan"}
				return
			}
			out[i] = deploySwitch(ctx, req, it)
			out[i].Wave = w.Index
			req.Logger.InfoContext(ctx, "switch deployed", "switch", m.Name, "wave", w.Index, "outcome", out[i].Outcome)
		})
	}
	wg.Wait()
	return out
}

// HealthCommands are the show commands a deployment gate compares.
func HealthCommands() []string {
	var out []string
	for _, p := range nxos.Probes() {
		switch p.Dimension {
		case device.DimNVEPeers, device.DimBGP, device.DimOSPF, device.DimVPC:
			out = append(out, p.Command)
		}
	}
	return out
}

func health(ctx context.Context, sw Switch) (rollout.Health, error) {
	outs, err := sw.Run(ctx, HealthCommands())
	if err != nil {
		return rollout.Health{}, err
	}
	s := nxos.Assemble(device.Identity{}, outs)
	return rollout.HealthOf(s.State), nil
}

// CheckpointName names the checkpoint taken before a bundle is applied.
func CheckpointName(bundle string, at time.Time) string {
	return "fabric-" + bundle + "-" + at.UTC().Format("20060102T150405")
}

func deploySwitch(ctx context.Context, req DeployRequest, it *Item) (res SwitchResult) {
	res = SwitchResult{Switch: it.Member.Name, Mgmt: it.Mgmt, Started: req.Now()}
	defer func() { res.Ended = req.Now() }()
	addr, err := netip.ParseAddr(it.Mgmt)
	if err != nil {
		res.Outcome, res.Detail = OutcomeFailed, "invalid management address"
		return res
	}
	sw := req.Dial(addr)
	before, err := health(ctx, sw)
	if err != nil {
		res.Outcome, res.Detail = OutcomeFailed, "pre-change health: "+session.Code(err)
		return res
	}
	res.Before = &before
	name := CheckpointName(req.Bundle.ID, req.Now())
	cfg, err := sw.Checkpoint(ctx, name)
	if err != nil {
		res.Outcome, res.Detail = OutcomeFailed, "checkpoint: "+session.Code(err)
		return res
	}
	res.Checkpoint = name
	if err := it.Plan.CheckBefore(cfg, cfg.Fingerprint); err != nil {
		res.Outcome, res.Detail = OutcomeDrifted, err.Error()
		return res
	}
	state, outcome := execute(ctx, req, it, sw)
	res.State, res.Outcome = &state, outcome
	if outcome != OutcomeVerified {
		res.Detail = state.Error
		return res
	}
	after, err := health(ctx, sw)
	if err != nil {
		res.Outcome, res.Detail = OutcomeDegraded, "post-change health: "+session.Code(err)
		return res
	}
	res.After = &after
	if lost := rollout.Gate(before, after); len(lost) > 0 {
		res.Outcome, res.Lost = OutcomeDegraded, lost
		return res
	}
	if req.Save {
		if err := sw.Save(ctx); err != nil {
			res.Detail = "save: " + session.Code(err)
			return res
		}
		res.Saved = true
	}
	return res
}

func execute(ctx context.Context, req DeployRequest, it *Item, sw Switch) (apply.State, Outcome) {
	env, err := it.Plan.Envelope()
	if err != nil {
		return apply.State{Error: err.Error()}, OutcomeFailed
	}
	body, err := it.Plan.MarshalJSON()
	if err != nil {
		return apply.State{Error: err.Error()}, OutcomeFailed
	}
	result, err := apply.Run(ctx, apply.Request[*device.Config]{
		Plan: planAdapter{it.Plan},
		Identity: apply.State{
			Device:               apply.Device{Host: env.Target.Host, Name: env.Target.Name, Platform: env.Target.Platform},
			PlanSHA256:           evidence.Digest(body),
			PlanItem:             req.Bundle.ID,
			ExpectedConfigSHA256: env.ExpectedFingerprint,
		},
		Open:     sw.Opener(),
		Journal:  req.Journal,
		Classify: session.Codes,
	})
	state := result.State
	if err != nil && state.Error == "" {
		state.Error = session.Code(err)
	}
	switch state.Status {
	case apply.StatusVerified:
		return state, OutcomeVerified
	case apply.StatusUncertain:
		return state, OutcomeUncertain
	case apply.StatusFailed, apply.StatusConfirmed, apply.StatusExecuting:
		return state, OutcomeFailed
	default:
		return state, OutcomeFailed
	}
}

// planAdapter presents a go-netops change plan to apply.Run.
type planAdapter struct {
	p *change.Plan[*device.Config]
}

func (a planAdapter) Commands() []string { return a.p.Commands() }

func (a planAdapter) CheckBefore(before *device.Config) error {
	return a.p.CheckBefore(before, before.Fingerprint)
}

func (a planAdapter) CheckAfter(before, after *device.Config) (string, error) {
	if err := a.p.Verify(before, after); err != nil {
		return after.Fingerprint, err
	}
	return after.Fingerprint, nil
}

// ErrRollback reports a rollback whose result could not be confirmed.
var ErrRollback = errors.New("workflow: rollback not confirmed")

// RollbackResult is a rollback and the health it left.
type RollbackResult struct {
	Fingerprint string         `json:"fingerprint"`
	Before      rollout.Health `json:"health_before"`
	After       rollout.Health `json:"health_after"`
	// Lost lists what the rollback itself lost; Recovered what it brought
	// back compared with before.
	Lost      []string `json:"lost,omitempty"`
	Recovered []string `json:"recovered,omitempty"`
}

// Rollback restores a checkpoint on one switch and re-checks health
// around it, so the operator sees whether service came back.
func Rollback(ctx context.Context, sw Switch, checkpoint string) (RollbackResult, error) {
	var res RollbackResult
	before, err := health(ctx, sw)
	if err != nil {
		return res, fmt.Errorf("%w: health before: %w", ErrRollback, err)
	}
	cfg, err := sw.Rollback(ctx, checkpoint)
	if err != nil {
		return res, fmt.Errorf("%w: %w", ErrRollback, err)
	}
	after, err := health(ctx, sw)
	if err != nil {
		return res, fmt.Errorf("%w: health after: %w", ErrRollback, err)
	}
	res.Fingerprint, res.Before, res.After = cfg.Fingerprint, before, after
	res.Lost, res.Recovered = rollout.Gate(before, after), rollout.Gate(after, before)
	return res, nil
}
