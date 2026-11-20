// Package baseline is the golden configuration every switch must follow
// regardless of the services it carries: enabled features, management
// settings (time, logging, AAA sources, terminal lines), vPC and NVE
// options, and NTP and syslog servers.
//
// A [Profile] layers expectations: defaults, then per role, then per
// region, then per role within a region. [Profile.Resolve] merges them for
// one switch. [Check] reports deviations in netops drift form, and
// [Expectation.Desired] turns the expectation into plan state, so the
// same planner that deploys intent also remediates baseline drift.
// [Derive] drafts a profile from the majority of running switches.
//
// The package performs no I/O.
package baseline
