package plan

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/gatework/go-netops/change"

	"github.com/gatework/fabric/device"
)

// ChangeKind is the go-netops change kind of a changeset.
const ChangeKind change.Kind = "fabric_changeset"

// ErrEmpty reports a changeset without operations.
var ErrEmpty = errors.New("plan: empty changeset")

// Renderer turns operations into device commands. The vendor package
// implements it; rollback is rendered from the inverted operations.
type Renderer interface {
	Render(ops []Op) ([]string, error)
}

// Changeset is the ordered operations for one switch, bound to a renderer.
// It implements change.Change and change.Preserving over *device.Config.
type Changeset struct {
	ops    []Op
	render Renderer
}

var (
	_ change.Change[*device.Config]     = (*Changeset)(nil)
	_ change.Preserving[*device.Config] = (*Changeset)(nil)
)

// NewChangeset binds ordered operations to a renderer.
func NewChangeset(ops []Op, r Renderer) (*Changeset, error) {
	if len(ops) == 0 {
		return nil, ErrEmpty
	}
	if r == nil {
		return nil, errors.New("plan: nil renderer")
	}
	return &Changeset{ops: Order(ops), render: r}, nil
}

// Ops returns a copy of the operations in execution order.
func (c *Changeset) Ops() []Op { return append([]Op(nil), c.ops...) }

// Kind identifies changesets.
func (c *Changeset) Kind() change.Kind { return ChangeKind }

// Validate checks the changeset in isolation.
func (c *Changeset) Validate() error {
	if len(c.ops) == 0 {
		return ErrEmpty
	}
	seen := map[Ref]bool{}
	for _, op := range c.ops {
		if seen[op.Ref()] {
			return fmt.Errorf("plan: %s changed twice", op.Ref())
		}
		seen[op.Ref()] = true
	}
	return nil
}

// Render returns the commands and the rollback, which inverts every
// operation in reverse order.
func (c *Changeset) Render() (change.Script, error) {
	cmds, err := c.render.Render(c.ops)
	if err != nil {
		return change.Script{}, err
	}
	inv := make([]Op, 0, len(c.ops))
	for _, op := range slices.Backward(c.ops) {
		inv = append(inv, op.Invert())
	}
	rollback, err := c.render.Render(inv)
	if err != nil {
		return change.Script{}, err
	}
	return change.Script{Commands: cmds, Rollback: rollback}, nil
}

// Eligible requires every target object to still be in its planned
// state. Operations are checked in order against the configuration the
// earlier operations leave, because later ones may depend on objects the
// earlier ones create.
func (c *Changeset) Eligible(cfg *device.Config) error {
	var errs []error
	sim := cfg.Clone()
	for _, op := range c.ops {
		if err := op.Current(sim); err != nil {
			errs = append(errs, err)
		}
		if err := op.Apply(sim); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Satisfied requires every target object to show its target state.
func (c *Changeset) Satisfied(cfg *device.Config) error {
	var errs []error
	for _, op := range c.ops {
		if err := op.Holds(cfg); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Preserved requires every other object to be unchanged.
func (c *Changeset) Preserved(before, after *device.Config) error {
	return Untouched(before, after, c.ops)
}

type wireOp struct {
	Kind Kind            `json:"kind"`
	Op   json.RawMessage `json:"op"`
}

// MarshalJSON encodes the operations with their kinds.
func (c *Changeset) MarshalJSON() ([]byte, error) {
	wire := make([]wireOp, 0, len(c.ops))
	for _, op := range c.ops {
		body, err := json.Marshal(op)
		if err != nil {
			return nil, fmt.Errorf("plan: encode %s: %w", op.Ref(), err)
		}
		wire = append(wire, wireOp{Kind: op.Ref().Kind, Op: body})
	}
	return json.Marshal(struct {
		Ops []wireOp `json:"ops"`
	}{wire})
}

// Decoder returns a go-netops change decoder that rebuilds changesets
// bound to r.
func Decoder(r Renderer) change.Decoder[*device.Config] {
	return func(kind change.Kind, body json.RawMessage) (change.Change[*device.Config], error) {
		if kind != ChangeKind {
			return nil, fmt.Errorf("plan: change kind %q is not executable", kind)
		}
		var wire struct {
			Ops []wireOp `json:"ops"`
		}
		if err := json.Unmarshal(body, &wire); err != nil {
			return nil, fmt.Errorf("plan: decode changeset: %w", err)
		}
		ops := make([]Op, 0, len(wire.Ops))
		for _, w := range wire.Ops {
			op, err := decodeOp(w)
			if err != nil {
				return nil, err
			}
			ops = append(ops, op)
		}
		return NewChangeset(ops, r)
	}
}

func decodeOp(w wireOp) (Op, error) {
	var op Op
	var err error
	switch w.Kind {
	case KindFeature:
		op, err = decodeAs[FeatureOp](w.Op)
	case KindSetting:
		op, err = decodeAs[SettingOp](w.Op)
	case KindNTPServer:
		op, err = decodeAs[NTPServerOp](w.Op)
	case KindLogServer:
		op, err = decodeAs[LogServerOp](w.Op)
	case KindVRF:
		op, err = decodeAs[VRFOp](w.Op)
	case KindVLAN:
		op, err = decodeAs[VLANOp](w.Op)
	case KindEVPN:
		op, err = decodeAs[EVPNOp](w.Op)
	case KindNVEMember:
		op, err = decodeAs[NVEMemberOp](w.Op)
	case KindSVI:
		op, err = decodeAs[SVIOp](w.Op)
	case KindBGPVRF:
		op, err = decodeAs[BGPVRFOp](w.Op)
	case KindPort, KindPortChannel:
		op, err = decodeAs[PortOp](w.Op)
	case KindInterface:
		op, err = decodeAs[InterfaceOp](w.Op)
	case KindOSPF:
		op, err = decodeAs[OSPFOp](w.Op)
	case KindBGP:
		op, err = decodeAs[BGPOp](w.Op)
	case KindBGPTemplate, KindBGPNeighbor:
		op, err = decodeAs[BGPPeerOp](w.Op)
	case KindNVE:
		op, err = decodeAs[NVEOp](w.Op)
	case KindVPCDomain:
		op, err = decodeAs[VPCDomainOp](w.Op)
	case KindInterfaceRst:
		op, err = decodeAs[ResetOp](w.Op)
	default:
		return nil, fmt.Errorf("plan: unknown operation kind %q", w.Kind)
	}
	if err != nil {
		return nil, fmt.Errorf("plan: decode %s operation: %w", w.Kind, err)
	}
	return op, nil
}

func decodeAs[T Op](body json.RawMessage) (Op, error) {
	var v T
	if err := json.Unmarshal(body, &v); err != nil {
		return nil, err
	}
	return v, nil
}
