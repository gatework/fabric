package device

import (
	"strconv"

	"github.com/gatework/fabric"
)

// Setting names one scalar configuration value that baselines compare and
// remediation changes, for example the timezone or a vPC domain option.
// Values are strings; boolean settings use [Enabled] or the empty string.
type Setting string

// Enabled is the value of a boolean setting that is on.
const Enabled = "enabled"

// Managed settings.
const (
	SettingHostname           Setting = "hostname"
	SettingTimezone           Setting = "timezone"
	SettingDomainName         Setting = "domain_name"
	SettingAnycastGatewayMAC  Setting = "anycast_gateway_mac"
	SettingNTPSource          Setting = "ntp.source_interface"
	SettingNTPLogging         Setting = "ntp.logging"
	SettingLoggingSource      Setting = "logging.source_interface"
	SettingLoggingOrigin      Setting = "logging.origin_id_hostname"
	SettingLoggingTimestamp   Setting = "logging.timestamp"
	SettingTACACSSource       Setting = "tacacs.source_interface"
	SettingVTYExecTimeout     Setting = "line.vty.exec_timeout"
	SettingVTYSessionLimit    Setting = "line.vty.session_limit"
	SettingConsoleExecTimeout Setting = "line.console.exec_timeout"
	SettingNVEVirtualRMAC     Setting = "nve.advertise_virtual_rmac"
	SettingBGPAdvertisePIP    Setting = "bgp.advertise_pip"
	SettingBGPRetainRT        Setting = "bgp.retain_route_target_all"
	SettingBGPLogNeighbor     Setting = "bgp.log_neighbor_changes"
	SettingVPCPeerSwitch      Setting = "vpc.peer_switch"
	SettingVPCPeerGateway     Setting = "vpc.peer_gateway"
	SettingVPCL3PeerRouter    Setting = "vpc.layer3_peer_router"
	SettingVPCAutoRecovery    Setting = "vpc.auto_recovery"
	SettingVPCFastConvergence Setting = "vpc.fast_convergence"
	SettingVPCARPSynchronize  Setting = "vpc.ip_arp_synchronize"
)

// Settings lists every managed setting in rendering order.
func Settings() []Setting {
	return []Setting{
		SettingHostname, SettingTimezone, SettingDomainName, SettingAnycastGatewayMAC,
		SettingNTPSource, SettingNTPLogging,
		SettingLoggingSource, SettingLoggingOrigin, SettingLoggingTimestamp,
		SettingTACACSSource,
		SettingVTYExecTimeout, SettingVTYSessionLimit, SettingConsoleExecTimeout,
		SettingNVEVirtualRMAC,
		SettingBGPAdvertisePIP, SettingBGPRetainRT, SettingBGPLogNeighbor,
		SettingVPCPeerSwitch, SettingVPCPeerGateway, SettingVPCL3PeerRouter,
		SettingVPCAutoRecovery, SettingVPCFastConvergence, SettingVPCARPSynchronize,
	}
}

// Valid reports whether s is a managed setting.
func (s Setting) Valid() bool {
	_, ok := specOf(s)
	return ok
}

// Boolean reports whether the setting is on/off rather than valued.
func (s Setting) Boolean() bool {
	sp, _ := specOf(s)
	return sp.boolean
}

// VPCOption returns the vPC domain option behind a vpc.* setting.
func (s Setting) VPCOption() (VPCOption, bool) {
	sp, _ := specOf(s)
	return sp.vpc, sp.vpc != ""
}

// Applicable reports whether the configuration has the object the setting
// lives on: vPC settings need a vPC domain, NVE settings an NVE interface,
// BGP settings a BGP process.
func (c *Config) Applicable(s Setting) bool {
	sp, ok := specOf(s)
	return ok && sp.on(c)
}

// Get returns the current value of a setting; the empty string means unset
// or off. Settings on objects the configuration lacks read as empty.
func (c *Config) Get(s Setting) string {
	sp, ok := specOf(s)
	if !ok || !sp.on(c) {
		return ""
	}
	return sp.get(c)
}

// Set changes a setting in the model. Setting a value on an object the
// configuration lacks is a no-op; callers check Applicable first.
func (c *Config) Set(s Setting, value string) {
	if sp, ok := specOf(s); ok && sp.on(c) {
		sp.set(c, value)
	}
}

// settingSpec binds a setting to the model field it reads and writes.
type settingSpec struct {
	boolean bool
	vpc     VPCOption
	// where returns false when the owning object is absent.
	where func(*Config) bool
	get   func(*Config) string
	set   func(*Config, string)
}

func (sp settingSpec) on(c *Config) bool { return sp.where == nil || sp.where(c) }

func flag(on bool) string {
	if on {
		return Enabled
	}
	return ""
}

func number(n int) string {
	if n == 0 {
		return ""
	}
	return strconv.Itoa(n)
}

func atoi(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}

func text(field func(*Config) *string) settingSpec {
	return settingSpec{
		get: func(c *Config) string { return *field(c) },
		set: func(c *Config, v string) { *field(c) = v },
	}
}

func toggle(where func(*Config) bool, field func(*Config) *bool) settingSpec {
	return settingSpec{
		boolean: true,
		where:   where,
		get:     func(c *Config) string { return flag(*field(c)) },
		set:     func(c *Config, v string) { *field(c) = v == Enabled },
	}
}

func count(field func(*Config) *int) settingSpec {
	return settingSpec{
		get: func(c *Config) string { return number(*field(c)) },
		set: func(c *Config, v string) { *field(c) = atoi(v) },
	}
}

func vpcOption(o VPCOption) settingSpec {
	return settingSpec{
		boolean: true,
		vpc:     o,
		where:   func(c *Config) bool { return c.VPC != nil },
		get:     func(c *Config) string { return flag(c.VPC.Option(o)) },
		set:     func(c *Config, v string) { c.VPC.SetOption(o, v == Enabled) },
	}
}

func hasNVE(c *Config) bool { return c.NVE != nil }
func hasBGP(c *Config) bool { return c.BGP != nil }

// specOf returns the binding of a managed setting.
func specOf(s Setting) (settingSpec, bool) {
	sp, ok := specs()[s]
	return sp, ok
}

// specs builds the setting table on demand so that the package keeps no
// mutable state.
func specs() map[Setting]settingSpec {
	return map[Setting]settingSpec{
		SettingHostname:   text(func(c *Config) *string { return &c.Hostname }),
		SettingTimezone:   text(func(c *Config) *string { return &c.Management.Timezone }),
		SettingDomainName: text(func(c *Config) *string { return &c.Management.DomainName }),
		SettingAnycastGatewayMAC: {
			get: func(c *Config) string { return string(c.AnycastGatewayMAC) },
			set: func(c *Config, v string) { c.AnycastGatewayMAC = fabric.MAC(v) },
		},
		SettingNTPSource:          text(func(c *Config) *string { return &c.Management.NTP.SourceInterface }),
		SettingNTPLogging:         toggle(nil, func(c *Config) *bool { return &c.Management.NTP.Logging }),
		SettingLoggingSource:      text(func(c *Config) *string { return &c.Management.Logging.SourceInterface }),
		SettingLoggingOrigin:      toggle(nil, func(c *Config) *bool { return &c.Management.Logging.OriginIDHostname }),
		SettingLoggingTimestamp:   text(func(c *Config) *string { return &c.Management.Logging.TimestampUnit }),
		SettingTACACSSource:       text(func(c *Config) *string { return &c.Management.TACACS.SourceInterface }),
		SettingVTYExecTimeout:     count(func(c *Config) *int { return &c.Management.VTYExecTimeout }),
		SettingVTYSessionLimit:    count(func(c *Config) *int { return &c.Management.VTYSessionLimit }),
		SettingConsoleExecTimeout: count(func(c *Config) *int { return &c.Management.ConsoleExecTimeout }),
		SettingNVEVirtualRMAC:     toggle(hasNVE, func(c *Config) *bool { return &c.NVE.AdvertiseVirtualRMAC }),
		SettingBGPAdvertisePIP:    toggle(hasBGP, func(c *Config) *bool { return &c.BGP.AdvertisePIP }),
		SettingBGPRetainRT:        toggle(hasBGP, func(c *Config) *bool { return &c.BGP.RetainRouteTargetAll }),
		SettingBGPLogNeighbor:     toggle(hasBGP, func(c *Config) *bool { return &c.BGP.LogNeighborChanges }),
		SettingVPCPeerSwitch:      vpcOption(VPCPeerSwitch),
		SettingVPCPeerGateway:     vpcOption(VPCPeerGateway),
		SettingVPCL3PeerRouter:    vpcOption(VPCL3PeerRouter),
		SettingVPCAutoRecovery:    vpcOption(VPCAutoRecovery),
		SettingVPCFastConvergence: vpcOption(VPCFastConvergence),
		SettingVPCARPSynchronize:  vpcOption(VPCARPSynchronize),
	}
}
