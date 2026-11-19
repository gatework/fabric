package baseline

import (
	"maps"
	"reflect"
	"slices"

	"github.com/gatework/fabric/device"
)

// Group is the configured switches of one region, used to derive a
// profile.
type Group struct {
	Region   string
	Switches []*device.Switch
}

// Derive drafts a profile from running switches. Per region, a setting or
// server is expected when a strict majority of the switches it applies to
// share a non-empty value; features are derived per role. Derivation never
// expects an option to be off. Values every region
// agrees on are promoted to the defaults. The draft records today's
// majority, not best practice: review it before enforcing.
func Derive(groups []Group) *Profile {
	p := &Profile{Schema: Schema, Roles: map[string]Expectation{}, Regions: map[string]RegionProfile{}}
	for _, g := range groups {
		p.Regions[g.Region] = deriveRegion(g.Switches)
	}
	promote(p)
	return p
}

func deriveRegion(switches []*device.Switch) RegionProfile {
	r := RegionProfile{Expectation: Expectation{Settings: map[device.Setting]string{}}, Roles: map[string]Expectation{}}
	var cfgs []*device.Config
	byRole := map[string][]*device.Config{}
	for _, sw := range switches {
		if !sw.Configured() {
			continue
		}
		cfgs = append(cfgs, sw.Config)
		byRole[sw.Role.String()] = append(byRole[sw.Role.String()], sw.Config)
	}
	for _, s := range device.Settings() {
		if intentOwned(s) {
			continue
		}
		var vals []string
		for _, c := range cfgs {
			if c.Applicable(s) {
				vals = append(vals, c.Get(s))
			}
		}
		// Only positive expectations are derived: a majority that lacks
		// an option says nothing about the switches that need it (vPC
		// options on vPC members, for example).
		if v, ok := majority(vals); ok && v != "" {
			r.Settings[s] = v
		}
	}
	r.NTPServers = majorityServers(cfgs, func(c *device.Config) []device.NTPServer { return c.Management.NTP.Servers },
		func(s device.NTPServer) string { return s.Address })
	r.LogServers = majorityServers(cfgs, func(c *device.Config) []device.LogServer { return c.Management.Logging.Servers },
		func(s device.LogServer) string { return s.Address })
	for role, list := range byRole {
		if role == "" {
			continue
		}
		r.Roles[role] = Expectation{Features: majorityFeatures(list)}
	}
	return r
}

// intentOwned reports settings the intent declares (per region), which a
// baseline must not duplicate.
func intentOwned(s device.Setting) bool { return s == device.SettingAnycastGatewayMAC }

// majority returns the value held by more than half of vals.
func majority[V comparable](vals []V) (V, bool) {
	counts := map[V]int{}
	for _, v := range vals {
		counts[v]++
	}
	for v, n := range counts {
		if 2*n > len(vals) {
			return v, true
		}
	}
	var zero V
	return zero, false
}

func majorityFeatures(cfgs []*device.Config) []string {
	counts := map[string]int{}
	for _, c := range cfgs {
		for f, on := range c.Features {
			if on {
				counts[f]++
			}
		}
	}
	var out []string
	for f, n := range counts {
		if 2*n > len(cfgs) {
			out = append(out, f)
		}
	}
	slices.Sort(out)
	return out
}

// majorityServers keeps servers present on most switches, each with its
// most common settings.
func majorityServers[S comparable](cfgs []*device.Config, list func(*device.Config) []S, key func(S) string) []S {
	byAddr := map[string][]S{}
	for _, c := range cfgs {
		for _, s := range list(c) {
			byAddr[key(s)] = append(byAddr[key(s)], s)
		}
	}
	var out []S
	for _, addr := range slices.Sorted(maps.Keys(byAddr)) {
		seen := byAddr[addr]
		if 2*len(seen) <= len(cfgs) {
			continue
		}
		if v, ok := majority(seen); ok {
			out = append(out, v)
		}
	}
	return out
}

// promote moves what every region agrees on into the defaults and roles.
func promote(p *Profile) {
	names := slices.Sorted(maps.Keys(p.Regions))
	if len(names) == 0 {
		return
	}
	first := p.Regions[names[0]]
	p.Default.Settings = map[device.Setting]string{}
	for s, v := range first.Settings {
		if allRegions(p, names, func(r RegionProfile) bool { val, ok := r.Settings[s]; return ok && val == v }) {
			p.Default.Settings[s] = v
		}
	}
	if allRegions(p, names, func(r RegionProfile) bool { return reflect.DeepEqual(r.NTPServers, first.NTPServers) }) {
		p.Default.NTPServers = first.NTPServers
	}
	if allRegions(p, names, func(r RegionProfile) bool { return reflect.DeepEqual(r.LogServers, first.LogServers) }) {
		p.Default.LogServers = first.LogServers
	}
	for role, e := range first.Roles {
		if allRegions(p, names, func(r RegionProfile) bool { return slices.Equal(r.Roles[role].Features, e.Features) }) {
			p.Roles[role] = Expectation{Features: e.Features}
		}
	}
	for _, name := range names {
		r := p.Regions[name]
		for s := range p.Default.Settings {
			delete(r.Settings, s)
		}
		if p.Default.NTPServers != nil {
			r.NTPServers = nil
		}
		if p.Default.LogServers != nil {
			r.LogServers = nil
		}
		for role := range p.Roles {
			delete(r.Roles, role)
		}
		p.Regions[name] = r
	}
}

func allRegions(p *Profile, names []string, ok func(RegionProfile) bool) bool {
	for _, n := range names {
		if !ok(p.Regions[n]) {
			return false
		}
	}
	return true
}
