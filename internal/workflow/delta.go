package workflow

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/gatework/netops/config"
	"github.com/gatework/netops/diff"
	"github.com/gatework/netops/parse"

	"github.com/gatework/fabric"
	"github.com/gatework/fabric/adapter/snapshot"
	"github.com/gatework/fabric/delta"
	"github.com/gatework/fabric/device"
	"github.com/gatework/fabric/internal/parallel"
)

// DiffRequest compares two snapshot directories.
type DiffRequest struct {
	From, To   string
	Intent     *fabric.Intent
	SubnetBits int
	// Switch limits the comparison to one switch; empty compares all.
	Switch string
}

// Diff reports what changed between two collections: configuration
// block by block, masked, and the operational state both observed.
func Diff(ctx context.Context, req DiffRequest) (delta.Report, error) {
	before, err := LoadSnapshot(ctx, req.From, req.Intent, LoadOptions{SubnetBits: req.SubnetBits})
	if err != nil {
		return delta.Report{}, fmt.Errorf("workflow: %s: %w", req.From, err)
	}
	after, err := LoadSnapshot(ctx, req.To, req.Intent, LoadOptions{SubnetBits: req.SubnetBits})
	if err != nil {
		return delta.Report{}, fmt.Errorf("workflow: %s: %w", req.To, err)
	}
	textA, err := runningConfigs(ctx, req.From)
	if err != nil {
		return delta.Report{}, err
	}
	textB, err := runningConfigs(ctx, req.To)
	if err != nil {
		return delta.Report{}, err
	}
	byKey := map[string]*delta.Pair{}
	var order []string
	add := func(sw *device.Switch, later bool) {
		k := sw.Key()
		p, ok := byKey[k]
		if !ok {
			p = &delta.Pair{}
			byKey[k] = p
			order = append(order, k)
		}
		p.Name, p.Region = sw.Name, sw.Region
		if later {
			p.After = sw
		} else {
			p.Before = sw
		}
	}
	for _, sw := range before.Switches {
		add(sw, false)
	}
	for _, sw := range after.Switches {
		add(sw, true)
	}
	var pairs []delta.Pair
	for _, k := range order {
		p := byKey[k]
		if req.Switch != "" && p.Name != req.Switch {
			continue
		}
		a, okA := textA[k]
		b, okB := textB[k]
		if okA && okB && p.Before != nil && p.After != nil {
			changes, err := configDiff(ctx, a, b)
			if err != nil {
				return delta.Report{}, fmt.Errorf("workflow: %s: %w", p.Name, err)
			}
			p.Config, p.ConfigKnown = changes, true
		}
		pairs = append(pairs, *p)
	}
	if req.Switch != "" && len(pairs) == 0 {
		return delta.Report{}, fmt.Errorf("workflow: %w: %s", ErrUnknownSwitch, req.Switch)
	}
	return delta.Compare(filepath.Base(req.From), filepath.Base(req.To), pairs), nil
}

// ErrUnknownSwitch reports a switch neither collection has.
var ErrUnknownSwitch = errors.New("unknown switch")

// runningConfigs reads every switch's running configuration by address.
func runningConfigs(ctx context.Context, dir string) (map[string]string, error) {
	dirs, err := snapshot.List(dir)
	if err != nil {
		return nil, err
	}
	keep := func(cmd string) bool { return cmd == "show running-config" }
	caps, errs := parallel.Map(ctx, 0, dirs, func(_ context.Context, d string) (snapshot.Capture, error) {
		return snapshot.ReadSome(d, keep)
	})
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, c := range caps {
		if o, ok := c.Outputs["show running-config"]; ok {
			out[c.Mgmt.String()] = o.Text
		}
	}
	return out, nil
}

// configDiff compares two running configurations block by block. ACL
// and route-map entries are order-sensitive; every line is masked.
func configDiff(ctx context.Context, a, b string) ([]diff.Change, error) {
	d := config.Cisco()
	docA, err := config.Parse(ctx, a, d, parse.Limits{})
	if err != nil {
		return nil, err
	}
	docB, err := config.Parse(ctx, b, d, parse.Limits{})
	if err != nil {
		return nil, err
	}
	changes := diff.Tree(docA, docB, diff.TreeOptions{
		Ordered: func(path []string) bool {
			return len(path) > 0 && (strings.HasPrefix(path[0], "ip access-list") || strings.HasPrefix(path[0], "ipv6 access-list"))
		},
	})
	for i := range changes {
		c := &changes[i]
		c.Line = d.Mask(c.Line)
		for j := range c.Block {
			c.Block[j] = d.Mask(c.Block[j])
		}
		for j := range c.Path {
			c.Path[j] = d.Mask(c.Path[j])
		}
	}
	return mergeMasked(changes), nil
}

// mergeMasked turns a removal and an addition that read the same once
// masked into one change: a secret (a key, a community) was changed.
func mergeMasked(changes []diff.Change) []diff.Change {
	key := func(c diff.Change) string { return strings.Join(c.Path, "\x00") + "\x00" + c.Line }
	added := map[string]int{}
	for i, c := range changes {
		if c.Kind == diff.KindAdded && strings.Contains(c.Line, "[REDACTED]") {
			added[key(c)] = i
		}
	}
	drop := map[int]bool{}
	for i := range changes {
		c := &changes[i]
		if c.Kind != diff.KindRemoved {
			continue
		}
		if j, ok := added[key(*c)]; ok && !drop[j] {
			c.Kind, c.LineB = diff.KindChanged, changes[j].LineB
			drop[j] = true
		}
	}
	out := changes[:0]
	for i, c := range changes {
		if !drop[i] {
			out = append(out, c)
		}
	}
	return out
}
