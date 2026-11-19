package plan

import (
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/gatework/fabric/device"
)

// Delta returns only the operations that moving from the before to the
// after desired state requires: operations the before state would also
// plan (drift that predates the change) are left out. An object that both
// drifted and is changed keeps its full operation.
//
// The result is proven like a full plan: after it, only operations the
// before state already planned may remain, and nothing else changes.
func Delta(before, after *Desired, cfg *device.Config, opt Options) ([]Op, error) {
	pre := Diff(before, cfg, opt)
	var ops []Op
	for _, op := range Diff(after, cfg, opt) {
		if !slices.ContainsFunc(pre, func(p Op) bool { return reflect.DeepEqual(p, op) }) {
			ops = append(ops, op)
		}
	}
	if len(ops) == 0 {
		return nil, nil
	}
	sim, err := Simulate(cfg, ops)
	if err != nil {
		return nil, err
	}
	var extra []string
	for _, op := range Diff(after, sim, opt) {
		if !slices.ContainsFunc(pre, func(p Op) bool { return p.Ref() == op.Ref() }) {
			extra = append(extra, op.Ref().String())
		}
	}
	if len(extra) > 0 {
		return nil, fmt.Errorf("%w: %s", ErrNotConverged, strings.Join(extra, ", "))
	}
	return ops, Untouched(cfg, sim, ops)
}
