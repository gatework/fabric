package baseline

import (
	"bytes"
	"fmt"
	"maps"
	"slices"

	"go.yaml.in/yaml/v3"

	"github.com/gatework/fabric"
	"github.com/gatework/fabric/device"
	"github.com/gatework/fabric/plan"
)

// Schema is the version of the profile document.
const Schema = 1

// Profile is a layered baseline.
type Profile struct {
	Schema  int                      `yaml:"schema" json:"schema"`
	Default Expectation              `yaml:"default" json:"default"`
	Roles   map[string]Expectation   `yaml:"roles,omitempty" json:"roles,omitempty"`
	Regions map[string]RegionProfile `yaml:"regions,omitempty" json:"regions,omitempty"`
}

// RegionProfile overrides the defaults for one region.
type RegionProfile struct {
	Expectation `yaml:",inline" json:",inline"`
	Roles       map[string]Expectation `yaml:"roles,omitempty" json:"roles,omitempty"`
}

// Expectation is what a switch must look like. An absent field is not
// managed; a setting listed with an empty value must be off or unset.
type Expectation struct {
	Features          []string                  `yaml:"features,omitempty" json:"features,omitempty"`
	ForbiddenFeatures []string                  `yaml:"forbidden_features,omitempty" json:"forbidden_features,omitempty"`
	Settings          map[device.Setting]string `yaml:"settings,omitempty" json:"settings,omitempty"`
	NTPServers        []device.NTPServer        `yaml:"ntp_servers,omitempty" json:"ntp_servers,omitempty"`
	LogServers        []device.LogServer        `yaml:"logging_servers,omitempty" json:"logging_servers,omitempty"`
	// ExclusiveServers makes the server lists complete: other servers
	// are deviations and remediation removes them.
	ExclusiveServers *bool `yaml:"exclusive_servers,omitempty" json:"exclusive_servers,omitempty"`
}

// Decode strictly decodes a YAML profile and validates its settings.
func Decode(body []byte) (*Profile, error) {
	dec := yaml.NewDecoder(bytes.NewReader(body))
	dec.KnownFields(true)
	var p Profile
	if err := dec.Decode(&p); err != nil {
		return nil, fmt.Errorf("baseline: decode: %w", err)
	}
	if p.Schema != Schema {
		return nil, fmt.Errorf("baseline: unsupported schema %d", p.Schema)
	}
	if err := p.validate(); err != nil {
		return nil, err
	}
	return &p, nil
}

// Encode renders the profile as YAML.
func Encode(p *Profile) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(p); err != nil {
		return nil, fmt.Errorf("baseline: encode: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("baseline: encode: %w", err)
	}
	return buf.Bytes(), nil
}

// Resolve merges the layers that apply to a switch of role in region.
func (p *Profile) Resolve(region string, role fabric.Role) Expectation {
	out := Expectation{Settings: map[device.Setting]string{}}
	out = out.overlay(p.Default)
	out = out.overlay(p.Roles[role.String()])
	if r, ok := p.Regions[region]; ok {
		out = out.overlay(r.Expectation)
		out = out.overlay(r.Roles[role.String()])
	}
	return out
}

// Desired returns the expectation as plan state for a switch whose
// configuration is cfg. Settings on objects the switch lacks (a vPC option
// on a switch without vPC) are left out.
func (x Expectation) Desired(cfg *device.Config) *plan.Desired {
	d := plan.NewDesired()
	for _, f := range x.Features {
		d.Features[f] = true
	}
	for _, f := range x.ForbiddenFeatures {
		d.Features[f] = false
	}
	for s, v := range x.Settings {
		if cfg.Applicable(s) {
			d.Settings[s] = v
		}
	}
	for _, s := range x.NTPServers {
		d.NTPServers[s.Address] = s
	}
	for _, s := range x.LogServers {
		d.LogServers[s.Address] = s
	}
	d.ExclusiveServers = x.ExclusiveServers != nil && *x.ExclusiveServers
	return d
}

func (p *Profile) validate() error {
	check := func(where string, e Expectation) error {
		for s := range e.Settings {
			if !s.Valid() {
				return fmt.Errorf("baseline: %s: unknown setting %q", where, s)
			}
		}
		return nil
	}
	if err := check("default", p.Default); err != nil {
		return err
	}
	for role, e := range p.Roles {
		if _, err := fabric.ParseRole(role); err != nil {
			return fmt.Errorf("baseline: roles: %w", err)
		}
		if err := check("roles."+role, e); err != nil {
			return err
		}
	}
	for name, r := range p.Regions {
		if err := check("regions."+name, r.Expectation); err != nil {
			return err
		}
		for role, e := range r.Roles {
			if err := check("regions."+name+".roles."+role, e); err != nil {
				return err
			}
		}
	}
	return nil
}

// overlay applies e on top of the receiver: settings merge key by key,
// lists given in e replace the receiver's.
func (x Expectation) overlay(e Expectation) Expectation {
	out := x
	out.Settings = maps.Clone(x.Settings)
	if out.Settings == nil {
		out.Settings = map[device.Setting]string{}
	}
	maps.Copy(out.Settings, e.Settings)
	if e.Features != nil {
		out.Features = slices.Clone(e.Features)
	}
	if e.ForbiddenFeatures != nil {
		out.ForbiddenFeatures = slices.Clone(e.ForbiddenFeatures)
	}
	if e.NTPServers != nil {
		out.NTPServers = slices.Clone(e.NTPServers)
	}
	if e.LogServers != nil {
		out.LogServers = slices.Clone(e.LogServers)
	}
	if e.ExclusiveServers != nil {
		v := *e.ExclusiveServers
		out.ExclusiveServers = &v
	}
	return out
}
