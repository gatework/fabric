package workflow

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"net/netip"
	"slices"

	"github.com/gatework/fabric"
	"github.com/gatework/fabric/adapter/snapshot"
	"github.com/gatework/fabric/adopt"
	"github.com/gatework/fabric/device"
	"github.com/gatework/fabric/vendors/nxos"
)

// Unassigned is the region of switches the intent does not declare.
const Unassigned = "unassigned"

// Fleet is every observed switch with the intent that names them.
type Fleet struct {
	Intent   *fabric.Intent
	Switches []*device.Switch
}

// LoadSnapshot reads a capture directory and assembles every switch. With
// an intent, switches take their name, region, role and model from it;
// without one, roles are inferred from configuration and regions are the
// management /bits subnets.
func LoadSnapshot(ctx context.Context, dir string, in *fabric.Intent, bits int) (*Fleet, error) {
	caps, err := snapshot.ReadAll(ctx, dir)
	if err != nil {
		return nil, err
	}
	f := &Fleet{Intent: in}
	for _, c := range caps {
		outs := make(map[string]string, len(c.Outputs))
		for cmd, o := range c.Outputs {
			outs[cmd] = o.Text
		}
		f.Switches = append(f.Switches, nxos.Assemble(device.Identity{Mgmt: c.Mgmt}, outs))
	}
	f.identify(bits)
	return f, nil
}

// Regions returns the region names in order.
func (f *Fleet) Regions() []string {
	set := map[string]bool{}
	for _, sw := range f.Switches {
		set[sw.Region] = true
	}
	return slices.Sorted(maps.Keys(set))
}

// Members returns the switches of a region.
func (f *Fleet) Members(region string) []*device.Switch {
	var out []*device.Switch
	for _, sw := range f.Switches {
		if sw.Region == region {
			out = append(out, sw)
		}
	}
	return out
}

// ByKey indexes switches by management address.
func (f *Fleet) ByKey() map[string]*device.Switch {
	out := make(map[string]*device.Switch, len(f.Switches))
	for _, sw := range f.Switches {
		out[sw.Key()] = sw
	}
	return out
}

// Switch returns the switch with name.
func (f *Fleet) Switch(name string) (*device.Switch, error) {
	for _, sw := range f.Switches {
		if sw.Name == name {
			return sw, nil
		}
	}
	return nil, fmt.Errorf("workflow: switch %q not found", name)
}

// identify fills identities from the intent or from subnets.
func (f *Fleet) identify(bits int) {
	for _, sw := range f.Switches {
		if f.Intent != nil {
			if r, s, ok := f.locate(sw.Mgmt); ok {
				sw.Name, sw.Region, sw.Role, sw.Model = s.Name, r.Name, s.Role, s.Model
				continue
			}
			sw.Region = Unassigned
			continue
		}
		if sw.Configured() {
			sw.Role = adopt.InferRole(sw.Config)
		}
		if p, err := sw.Mgmt.Prefix(bits); err == nil && bits > 0 {
			sw.Region = p.String()
		} else {
			sw.Region = Unassigned
		}
	}
	slices.SortFunc(f.Switches, func(a, b *device.Switch) int {
		return cmp.Or(cmp.Compare(a.Region, b.Region), cmp.Compare(a.Name, b.Name), a.Mgmt.Compare(b.Mgmt))
	})
}

func (f *Fleet) locate(mgmt netip.Addr) (*fabric.Region, *fabric.Switch, bool) {
	for _, r := range f.Intent.Regions {
		for _, s := range r.Switches {
			if s.Mgmt == mgmt {
				return r, s, true
			}
		}
	}
	return nil, nil, false
}
