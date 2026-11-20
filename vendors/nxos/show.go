package nxos

import (
	"context"
	"strings"
	"unicode/utf8"

	"github.com/gatework/netops/evidence"
	"github.com/gatework/netops/parse"

	"github.com/gatework/fabric/device"
)

// Issue codes recorded by the show parsers.
const (
	IssueUnparsedLine    = "unparsed_line"
	IssueUnsupported     = "command_unsupported"
	IssueInvalidUTF8     = "invalid_utf8"
	IssueHeaderMissing   = "header_missing"
	IssueCountMismatch   = "count_mismatch"
	IssueConfigMalformed = "configuration_malformed"
)

// result is what one show parser produced.
type result struct {
	scan parse.Scanner
	// header is set once a table header was recognized; a non-empty
	// output without a header is not understood.
	header bool
	empty  bool
}

func (r *result) status() evidence.Status {
	if !r.header && !r.empty {
		r.scan.Issue(IssueHeaderMissing, 0)
		return evidence.StatusPartial
	}
	return r.scan.Status(true)
}

// rejected reports output that says the command is unknown or the feature
// is off.
func rejected(text string) bool {
	for line := range strings.SplitSeq(text, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "% Invalid") || strings.HasPrefix(t, "% Incomplete") ||
			strings.Contains(t, "not enabled") || strings.HasPrefix(t, "Syntax error") {
			return true
		}
	}
	return false
}

func blank(text string) bool { return strings.TrimSpace(text) == "" }

// dashes reports a separator row made of '-', '+' and spaces.
func dashes(t string) bool {
	return t != "" && strings.Trim(t, "-+ ") == ""
}

// Observe parses the output of the command collected for dim and records
// it in the switch: the state table, the coverage status and issues.
func Observe(ctx context.Context, sw *device.Switch, dim evidence.Dimension, text string) {
	if sw.Issues == nil {
		sw.Issues = map[evidence.Dimension][]evidence.Issue{}
	}
	status, issues := observe(ctx, sw, dim, text)
	sw.Coverage = sw.Coverage.With(dim, status)
	if len(issues) > 0 {
		sw.Issues[dim] = issues
	}
}

func observe(ctx context.Context, sw *device.Switch, dim evidence.Dimension, text string) (evidence.Status, []evidence.Issue) {
	if !utf8.ValidString(text) {
		return evidence.StatusFailed, []evidence.Issue{{Code: IssueInvalidUTF8}}
	}
	if rejected(text) {
		return evidence.StatusUnsupported, []evidence.Issue{{Code: IssueUnsupported}}
	}
	if dim == device.DimConfig {
		cfg, err := ParseConfig(ctx, text)
		if err != nil {
			return evidence.StatusFailed, []evidence.Issue{{Code: IssueConfigMalformed}}
		}
		sw.Config = cfg
		if sw.Name == "" {
			sw.Name = cfg.Hostname
		}
		return evidence.StatusComplete, nil
	}
	parse, ok := showParser(dim)
	if !ok {
		return evidence.StatusUnsupported, []evidence.Issue{{Code: IssueUnsupported}}
	}
	r := parse(&sw.State, text)
	return r.status(), r.scan.Issues()
}

func showParser(dim evidence.Dimension) (func(*device.State, string) *result, bool) {
	switch dim {
	case device.DimNVEPeers:
		return parseNVEPeers, true
	case device.DimNVEVNIs:
		return parseNVEVNIs, true
	case device.DimBGP:
		return parseBGPSummary, true
	case device.DimOSPF:
		return parseOSPF, true
	case device.DimVPC:
		return parseVPC, true
	case device.DimInterfaces:
		return parseInterfaces, true
	case device.DimPortChannels:
		return parsePortChannels, true
	case device.DimMACs:
		return parseMACs, true
	case device.DimL2Routes:
		return parseL2Routes, true
	case device.DimMACIPs:
		return parseMACIPs, true
	case device.DimCDP:
		return parseCDP, true
	default:
		return nil, false
	}
}
