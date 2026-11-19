package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gatework/go-netops/change"
	"github.com/gatework/go-netops/store"

	"github.com/gatework/fabric/device"
	"github.com/gatework/fabric/plan"
	"github.com/gatework/fabric/vendors/nxos"
)

// ErrTampered reports a plan directory whose contents no longer match
// its manifest.
var ErrTampered = errors.New("workflow: plan bundle was modified after planning")

const manifestName = "manifest.json"

type manifest struct {
	Bundle
	Files map[string]string `json:"files"`
}

// WriteBundle publishes the bundle into the store directory: the
// manifest, one go-netops plan envelope per switch, and review scripts.
func WriteBundle(ctx context.Context, st *store.Store, b *Bundle) error {
	m := manifest{Bundle: *b, Files: map[string]string{}}
	for _, it := range b.Items {
		name := safe(it.Member.Name)
		rel := "plans/" + name + ".json"
		if err := st.PublishJSON(ctx, rel, it.Plan); err != nil {
			return fmt.Errorf("workflow: %w", err)
		}
		m.Files[it.Member.Name] = rel
		if err := st.Publish(ctx, "scripts/"+name+".cfg", []byte(it.Review)); err != nil {
			return fmt.Errorf("workflow: %w", err)
		}
		rollback := strings.Join(it.Plan.Rollback(), "\n") + "\n"
		if err := st.Publish(ctx, "scripts/"+name+".rollback.txt", []byte(rollback)); err != nil {
			return fmt.Errorf("workflow: %w", err)
		}
	}
	if err := st.PublishJSON(ctx, manifestName, m); err != nil {
		return fmt.Errorf("workflow: %w", err)
	}
	return nil
}

func safe(name string) string {
	return strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r == ' ' {
			return '_'
		}
		return r
	}, name)
}

// ReadBundle loads a plan directory. Every plan is re-rendered by go-netops
// and the bundle identifier recomputed; any edit is rejected.
func ReadBundle(dir string) (*Bundle, error) {
	body, err := os.ReadFile(filepath.Join(dir, manifestName))
	if err != nil {
		return nil, fmt.Errorf("workflow: %w", err)
	}
	var m manifest
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, fmt.Errorf("workflow: manifest: %w", err)
	}
	b := m.Bundle
	for _, it := range b.Items {
		rel, ok := m.Files[it.Member.Name]
		if !ok || strings.Contains(rel, "..") {
			return nil, fmt.Errorf("%w: %s has no plan file", ErrTampered, it.Member.Name)
		}
		raw, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil {
			return nil, fmt.Errorf("workflow: %w", err)
		}
		p, err := change.DecodePlan(raw, plan.Decoder(nxos.Renderer{}))
		if err != nil {
			return nil, fmt.Errorf("%w: %s: %w", ErrTampered, it.Member.Name, err)
		}
		if p.Target().Name != it.Member.Name || p.Target().Host != it.Mgmt {
			return nil, fmt.Errorf("%w: %s plan targets another switch", ErrTampered, it.Member.Name)
		}
		it.Plan = p
		it.Ops = changesetOps(p)
	}
	id, err := bundleID(&b)
	if err != nil {
		return nil, err
	}
	if id != b.ID {
		return nil, fmt.Errorf("%w: id %s, contents %s", ErrTampered, b.ID, id)
	}
	return &b, nil
}

func changesetOps(p *change.Plan[*device.Config]) []plan.Op {
	if cs, ok := p.Change().(*plan.Changeset); ok {
		return cs.Ops()
	}
	return nil
}
