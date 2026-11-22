package cli

import (
	"context"
	"time"

	"github.com/gatework/netops/authz"
	ndevice "github.com/gatework/netops/device"
	"github.com/gatework/netops/lease"

	"github.com/gatework/fabric/internal/bootstrap"
)

// guard holds what protects a change: who the operator is, what the
// authorization policy allows, and the leases shared with other tools.
type guard struct {
	actor  string
	policy *authz.Policy
	leases *lease.Manager
}

func newGuard(e env, authzPath string) (*guard, error) {
	policy, err := bootstrap.ReadAuthz(authzPath)
	if err != nil {
		return nil, err
	}
	leases, err := bootstrap.Leases(e.environ, e.home)
	if err != nil {
		return nil, err
	}
	actor, _ := e.lookup("USER")
	return &guard{actor: actor, policy: policy, leases: leases}, nil
}

// authorize returns the check of perm, or nil without a policy.
func (g *guard) authorize(perm authz.Permission) func(ndevice.Device) error {
	if g.policy == nil {
		return func(ndevice.Device) error { return nil }
	}
	return func(d ndevice.Device) error { return g.policy.Authorize(g.actor, perm, d) }
}

func (g *guard) holder(purpose string) lease.Holder {
	return lease.Holder{Tool: "fabric", Actor: g.actor, Purpose: purpose}
}

// hold leases one device for an operation and returns its release.
func (g *guard) hold(ctx context.Context, d ndevice.Device, purpose string) (func(), error) {
	set, err := g.leases.Acquire(ctx, []string{lease.Key(d)}, g.holder(purpose), time.Hour)
	if err != nil {
		return nil, err
	}
	return func() { _ = g.leases.Release(context.WithoutCancel(ctx), set) }, nil
}
