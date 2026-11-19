package workflow

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/gatework/fabric"
	"github.com/gatework/fabric/governance"
	"github.com/gatework/fabric/provision"
)

// ProvisionRequest applies one operator request to the intent.
type ProvisionRequest struct {
	Intent  *fabric.Intent
	Request provision.Request
	// Fleet, when given, is checked for collisions and planned.
	Fleet *Fleet
	Now   time.Time
	// Change is the change record of the resulting plan.
	Change governance.Record
}

// Provisioned is a changed intent and, with a fleet, its plan.
type Provisioned struct {
	Intent *fabric.Intent
	Result provision.Result
	// Bundle plans the affected observed switches; nil without a fleet.
	Bundle *Bundle
	// Unobserved are affected switches without evidence, such as a new
	// switch that needs its day-0 configuration.
	Unobserved []string
}

// Provision applies the request and, given a fleet, plans exactly the
// switches it affects, including the targeted removals it asks for.
func Provision(req ProvisionRequest) (*Provisioned, error) {
	var obs *provision.Observation
	if req.Fleet != nil {
		obs = provision.NewObservation(req.Fleet.Switches)
	}
	in, res, err := provision.Apply(req.Intent, req.Request, obs)
	if err != nil {
		return nil, err
	}
	out := &Provisioned{Intent: in, Result: res}
	if req.Fleet == nil {
		return out, nil
	}
	fleet := &Fleet{Intent: in, Switches: req.Fleet.Switches}
	fleet.identify(0)
	var observed []string
	for _, name := range res.Affected {
		if sw, err := fleet.Switch(name); err == nil && sw.Configured() {
			observed = append(observed, name)
		} else {
			out.Unobserved = append(out.Unobserved, name)
		}
	}
	if len(observed) == 0 {
		return out, nil
	}
	out.Bundle, err = Plan(PlanRequest{
		Fleet: fleet, Switches: observed, Reset: res.Reset, Networks: res.RemovedNetworks,
		Removed: res.RemovedNeighbors, Now: req.Now, Before: req.Intent, Change: req.Change,
	})
	return out, err
}

// WriteIntent replaces the intent file, keeping the previous version as
// path + ".bak".
func WriteIntent(path string, in *fabric.Intent) error {
	body, err := fabric.EncodeIntent(in)
	if err != nil {
		return err
	}
	path = filepath.Clean(path)
	if old, err := os.ReadFile(path); err == nil {
		//nolint:gosec // G703: the operator names the intent file to update.
		if err := os.WriteFile(path+".bak", old, 0o600); err != nil {
			return fmt.Errorf("workflow: backup intent: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("workflow: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		return fmt.Errorf("workflow: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("workflow: %w", err)
	}
	return nil
}
