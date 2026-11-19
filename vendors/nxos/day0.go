package nxos

import (
	"maps"
	"slices"
	"strings"

	"github.com/gatework/fabric/device"
	"github.com/gatework/fabric/plan"
)

// Day0 renders the complete configuration of a factory-fresh switch for
// the desired state, for POAP or console paste. Physical ports the state
// mentions are assumed present and unconfigured, as switchports, so that
// routed links say "no switchport" whatever the platform default is.
// Prerequisites that need
// a reload are listed as comments at the top.
func Day0(d *plan.Desired) (string, error) {
	cfg := device.NewConfig()
	cfg.Management.DomainLookup = true
	names := slices.Sorted(maps.Keys(d.Interfaces))
	names = append(names, slices.Sorted(maps.Keys(d.Ports))...)
	for _, name := range names {
		if canonical, kind := device.CanonicalInterface(name); kind == device.KindEthernet {
			cfg.Interfaces[canonical] = device.Interface{Name: canonical, Kind: kind, Layer2: true}
		}
	}
	ops := plan.Diff(d, cfg, plan.Options{})
	if err := plan.Converges(d, cfg, ops, plan.Options{}); err != nil {
		return "", err
	}
	body, err := RenderConfig(ops)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	_, _ = b.WriteString("! fabric day-0 configuration\n")
	if d.VPC != nil && d.VPC.VirtualPeerLink != "" {
		_, _ = b.WriteString("! prerequisite: virtual peer-link needs a TCAM region, for example\n")
		_, _ = b.WriteString("!   hardware access-list tcam region ing-flow-redirect 512\n")
		_, _ = b.WriteString("! (free space from another region first), then reload before applying.\n")
	}
	_, _ = b.WriteString(body)
	return b.String(), nil
}
