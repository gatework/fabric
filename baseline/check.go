package baseline

import (
	"maps"
	"slices"

	"github.com/gatework/go-netops/assess"
	"github.com/gatework/go-netops/drift"

	"github.com/gatework/fabric/device"
)

// Check compares a configuration with an expectation. Deviations are
// ordered: features, settings, then servers.
func Check(subject assess.Subject, x Expectation, cfg *device.Config) []drift.Deviation {
	var out []drift.Deviation
	for _, f := range x.Features {
		if !cfg.Features[f] {
			out = append(out, deviation(subject, "feature."+f, drift.KindMissing, device.Enabled, ""))
		}
	}
	for _, f := range x.ForbiddenFeatures {
		if cfg.Features[f] {
			out = append(out, deviation(subject, "feature."+f, drift.KindUnexpected, "", device.Enabled))
		}
	}
	for _, s := range slices.Sorted(maps.Keys(x.Settings)) {
		if !cfg.Applicable(s) {
			continue
		}
		want, have := x.Settings[s], cfg.Get(s)
		if want == have {
			continue
		}
		if d, ok := drift.Expect(subject, string(s), want, have, have != ""); ok {
			d.Source = drift.SourceBaseline
			out = append(out, d)
		}
	}
	out = append(out, servers(subject, x, cfg)...)
	return out
}

func deviation(subject assess.Subject, path string, kind drift.Kind, want, have string) drift.Deviation {
	return drift.Deviation{Subject: subject, Path: path, Kind: kind, Source: drift.SourceBaseline, Expected: want, Actual: have}
}

func servers(subject assess.Subject, x Expectation, cfg *device.Config) []drift.Deviation {
	var out []drift.Deviation
	for _, want := range x.NTPServers {
		have, ok := cfg.Management.NTP.Server(want.Address)
		if d, bad := drift.Expect(subject, "ntp_server."+want.Address, want, have, ok); bad {
			d.Source = drift.SourceBaseline
			out = append(out, d)
		}
	}
	for _, want := range x.LogServers {
		have, ok := cfg.Management.Logging.Server(want.Address)
		if d, bad := drift.Expect(subject, "logging_server."+want.Address, want, have, ok); bad {
			d.Source = drift.SourceBaseline
			out = append(out, d)
		}
	}
	if x.ExclusiveServers == nil || !*x.ExclusiveServers {
		return out
	}
	for _, s := range cfg.Management.NTP.Servers {
		if !slices.ContainsFunc(x.NTPServers, func(w device.NTPServer) bool { return w.Address == s.Address }) {
			out = append(out, drift.Deviation{Subject: subject, Path: "ntp_server." + s.Address, Kind: drift.KindUnexpected, Source: drift.SourceBaseline, Actual: s})
		}
	}
	for _, s := range cfg.Management.Logging.Servers {
		if !slices.ContainsFunc(x.LogServers, func(w device.LogServer) bool { return w.Address == s.Address }) {
			out = append(out, drift.Deviation{Subject: subject, Path: "logging_server." + s.Address, Kind: drift.KindUnexpected, Source: drift.SourceBaseline, Actual: s})
		}
	}
	return out
}
