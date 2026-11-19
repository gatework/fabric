package nxos

import (
	"errors"
	"fmt"
	"strings"

	"github.com/gatework/go-netops/evidence"

	"github.com/gatework/fabric/device"
)

// Platform is the net-connector platform name for NX-OS.
const Platform = "cisco_nxos"

// ErrUnknownDimension reports a dimension without an NX-OS command.
var ErrUnknownDimension = errors.New("nxos: unknown dimension")

// Probe pairs a dimension with the command that observes it.
type Probe struct {
	Dimension evidence.Dimension
	Command   string
}

// Probes lists every dimension's command in collection order. The
// configuration comes first because it decides which features exist.
func Probes() []Probe {
	return []Probe{
		{device.DimConfig, "show running-config"},
		{device.DimCDP, "show cdp neighbors detail"},
		{device.DimNVEPeers, "show nve peers"},
		{device.DimNVEVNIs, "show nve vni"},
		{device.DimBGP, "show bgp l2vpn evpn summary"},
		{device.DimOSPF, "show ip ospf neighbors"},
		{device.DimVPC, "show vpc brief"},
		{device.DimInterfaces, "show interface brief"},
		{device.DimPortChannels, "show port-channel summary"},
		{device.DimMACs, "show mac address-table"},
		{device.DimL2Routes, "show l2route evpn mac all"},
		{device.DimMACIPs, "show l2route evpn mac-ip all"},
	}
}

// Preparation lists the commands that make output unpaged and unwrapped.
func Preparation() []string { return []string{"terminal length 0", "terminal width 511"} }

// Profile resolves NX-OS commands for dimensions. It satisfies
// assess.VerifyCommands.
type Profile struct{}

// Command returns the show command that observes dim.
func (Profile) Command(dim evidence.Dimension) (string, error) {
	for _, p := range Probes() {
		if p.Dimension == dim {
			return p.Command, nil
		}
	}
	return "", fmt.Errorf("%w: %s", ErrUnknownDimension, dim)
}

// Absent reports whether the configuration proves the dimension has
// nothing to show, so it need not be collected: no vPC domain, no NVE
// interface, no port-channel, no OSPF process.
func Absent(cfg *device.Config, dim evidence.Dimension) bool {
	if cfg == nil {
		return false
	}
	switch dim {
	case device.DimVPC:
		return cfg.VPC == nil
	case device.DimNVEPeers, device.DimNVEVNIs, device.DimL2Routes, device.DimMACIPs:
		return cfg.NVE == nil
	case device.DimOSPF:
		return len(cfg.OSPF) == 0
	case device.DimBGP:
		return cfg.BGP == nil
	default:
		return false
	}
}

// Assemble builds a switch from command outputs keyed by command. A
// dimension whose command is missing is complete when the configuration
// proves it absent, and not requested otherwise.
func Assemble(id device.Identity, outputs map[string]string) *device.Switch {
	sw := &device.Switch{Identity: id, Coverage: evidence.Coverage{}}
	for _, p := range Probes() {
		text, ok := outputs[p.Command]
		switch {
		case ok:
			Observe(sw, p.Dimension, text)
		case Absent(sw.Config, p.Dimension):
			sw.Coverage[p.Dimension] = evidence.StatusComplete
		default:
			sw.Coverage[p.Dimension] = evidence.StatusNotRequested
		}
	}
	return sw
}

// CheckpointCommand returns the exec command that records a checkpoint.
func CheckpointCommand(name string) string { return "checkpoint " + name }

// RollbackCommand returns the exec command that atomically restores a
// checkpoint: either every difference applies or none does.
func RollbackCommand(name string) string {
	return "rollback running-config checkpoint " + name + " atomic"
}

// Confirmed reports whether checkpoint or rollback output shows success.
func Confirmed(output string) bool {
	lower := strings.ToLower(output)
	if strings.Contains(lower, "fail") || strings.Contains(lower, "error") || strings.Contains(lower, "invalid") {
		return false
	}
	return strings.Contains(lower, "done") || strings.Contains(lower, "success") || strings.Contains(lower, "completed")
}
