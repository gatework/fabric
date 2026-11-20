package plan

import (
	"fmt"

	"github.com/gatework/netops/change"
	ndevice "github.com/gatework/netops/device"
	"github.com/gatework/netops/evidence"

	"github.com/gatework/fabric/device"
)

// Fingerprint identifies a running configuration for drift detection.
func Fingerprint(c *device.Config) evidence.Digest {
	if c == nil {
		return ""
	}
	return evidence.Digest(c.Fingerprint)
}

// Codec decodes changeset plan envelopes bound to r.
func Codec(r Renderer) (*change.Codec[*device.Config], error) {
	c, err := change.NewCodec(Fingerprint, Decoder(r))
	if err != nil {
		return nil, fmt.Errorf("plan: %w", err)
	}
	return c, nil
}

// NewPlan binds a changeset to a switch and the configuration it was
// planned against.
func NewPlan(meta change.Meta, target ndevice.Device, cfg *device.Config, cs *Changeset) (*change.Plan[*device.Config], error) {
	p, err := change.NewPlan(meta, target, cfg, change.Change[*device.Config](cs), Fingerprint)
	if err != nil {
		return nil, fmt.Errorf("plan: %w", err)
	}
	return p, nil
}

// ChangesetOf returns the changeset of a plan.
func ChangesetOf(p *change.Plan[*device.Config]) (*Changeset, bool) {
	cs, ok := p.Change().(*Changeset)
	return cs, ok
}
