package provision

import (
	"errors"
	"fmt"
	"net/netip"
	"slices"

	"github.com/gatework/fabric"
	"github.com/gatework/fabric/device"
)

// Errors common to requests.
var (
	ErrNotFound = errors.New("provision: not found")
	ErrConflict = errors.New("provision: conflict")
	ErrInUse    = errors.New("provision: in use")
	// ErrActive reports a port that nothing configures but that is in
	// use on the switch: its link is up or it learns MAC addresses.
	ErrActive   = errors.New("provision: port active")
	ErrUnderlay = errors.New("provision: region does not manage its underlay")
)

// Request is one operator change.
type Request interface {
	// Apply changes in, which is a private copy, and reports what it did.
	Apply(in *fabric.Intent, obs *Observation) (Result, error)
}

// Change is one human-readable change to the intent.
type Change struct {
	Subject string `json:"subject"`
	Detail  string `json:"detail"`
}

// Allocation is one value a request chose.
type Allocation struct {
	What  string `json:"what"`
	Value string `json:"value"`
}

// Result describes what a request did and what planning must cover.
type Result struct {
	Region    string       `json:"region"`
	Changes   []Change     `json:"changes"`
	Allocated []Allocation `json:"allocated,omitempty"`
	// Affected are the switches whose configuration changes.
	Affected []string `json:"affected"`
	// Reset lists ports to return to factory state, per switch.
	Reset map[string][]string `json:"reset,omitempty"`
	// RemovedNetworks maps a retired VLAN to its VNI for targeted deletion.
	RemovedNetworks map[fabric.VLAN]fabric.VNI `json:"removed_networks,omitempty"`
	// RemovedNeighbors lists BGP neighbors to delete, per switch.
	RemovedNeighbors map[string][]string `json:"removed_neighbors,omitempty"`
}

func (r *Result) change(subject, format string, args ...any) {
	r.Changes = append(r.Changes, Change{Subject: subject, Detail: fmt.Sprintf(format, args...)})
}

func (r *Result) allocate(what string, value any) {
	r.Allocated = append(r.Allocated, Allocation{What: what, Value: fmt.Sprint(value)})
}

func (r *Result) affect(names ...string) {
	for _, n := range names {
		if !slices.Contains(r.Affected, n) {
			r.Affected = append(r.Affected, n)
		}
	}
	slices.Sort(r.Affected)
}

func (r *Result) reset(sw string, ports ...string) {
	if r.Reset == nil {
		r.Reset = map[string][]string{}
	}
	r.Reset[sw] = append(r.Reset[sw], ports...)
	r.affect(sw)
}

// Apply runs the request on a copy of the intent and returns the copy if
// the result is a valid intent.
func Apply(in *fabric.Intent, req Request, obs *Observation) (*fabric.Intent, Result, error) {
	out, err := in.Clone()
	if err != nil {
		return nil, Result{}, err
	}
	res, err := req.Apply(out, obs)
	if err != nil {
		return nil, res, err
	}
	if err := out.Validate(); err != nil {
		return nil, res, err
	}
	return out, res, nil
}

func region(in *fabric.Intent, name string) (*fabric.Region, error) {
	r, ok := in.Region(name)
	if !ok {
		return nil, fmt.Errorf("%w: region %s", ErrNotFound, name)
	}
	return r, nil
}

// regionOf returns the region of the named switch.
func regionOf(in *fabric.Intent, name string) (*fabric.Region, *fabric.Switch, error) {
	r, s, ok := in.Locate(name)
	if !ok {
		return nil, nil, fmt.Errorf("%w: switch %s", ErrNotFound, name)
	}
	return r, s, nil
}

// Observation is what the switches show today, used to reject changes
// that collide with configuration the intent does not describe. A nil
// Observation checks the intent only.
type Observation struct {
	byName map[string]*device.Switch
}

// NewObservation indexes switches by name.
func NewObservation(switches []*device.Switch) *Observation {
	o := &Observation{byName: map[string]*device.Switch{}}
	for _, sw := range switches {
		if sw.Configured() {
			o.byName[sw.Name] = sw
		}
	}
	return o
}

func (o *Observation) config(name string) (*device.Config, bool) {
	if o == nil {
		return nil, false
	}
	sw, ok := o.byName[name]
	if !ok {
		return nil, false
	}
	return sw.Config, true
}

func (o *Observation) state(name string) (device.State, bool) {
	if o == nil {
		return device.State{}, false
	}
	sw, ok := o.byName[name]
	if !ok {
		return device.State{}, false
	}
	return sw.State, true
}

// addresses returns every interface address configured on the region's
// observed switches.
func (o *Observation) addresses(r *fabric.Region) []netip.Prefix {
	var out []netip.Prefix
	for _, s := range r.Switches {
		cfg, ok := o.config(s.Name)
		if !ok {
			continue
		}
		for _, i := range cfg.Interfaces {
			out = append(out, i.Addresses...)
			out = append(out, i.Secondary...)
		}
	}
	return out
}

// vlans returns the VLANs configured on the region's observed switches.
func (o *Observation) vlans(r *fabric.Region) []fabric.VLAN {
	var out []fabric.VLAN
	for _, s := range r.Switches {
		if cfg, ok := o.config(s.Name); ok {
			for v := range cfg.VLANs {
				out = append(out, v)
			}
		}
	}
	return out
}
