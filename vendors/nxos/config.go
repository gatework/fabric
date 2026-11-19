package nxos

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/gatework/go-netops/evidence"
	"github.com/gatework/parser"

	"github.com/gatework/fabric/device"
)

// ErrMalformed reports input that cannot be configuration text at all.
var ErrMalformed = errors.New("nxos: malformed input")

// Limits bounds accepted configuration text.
var configLimits = parser.Limits{MaxBytes: 32 << 20, MaxLines: 500_000}

// ParseConfig parses "show running-config" output. Lines the model does not
// cover are counted by keyword in Config.Unparsed and never fail the parse.
func ParseConfig(text string) (*device.Config, error) {
	if !utf8.ValidString(text) {
		return nil, fmt.Errorf("%w: invalid UTF-8", ErrMalformed)
	}
	content, err := parser.New(text, configLimits)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrMalformed, err)
	}
	cfg := device.NewConfig()
	cfg.Fingerprint = evidence.Digest([]byte(fingerprintText(text)))
	cfg.Management.DomainLookup = true
	p := &configParser{cfg: cfg}
	for _, n := range buildTree(content) {
		p.top(n)
	}
	p.finish()
	return cfg, nil
}

type configParser struct {
	cfg *device.Config
	// vlanList holds VLANs declared only by "vlan 1,10-20".
	vlanList []string
}

func (p *configParser) unparsed(n *node) {
	if p.cfg.Unparsed == nil {
		p.cfg.Unparsed = map[string]int{}
	}
	key := n.arg(0)
	if len(n.fields) > 1 && (key == "ip" || key == "no" || key == "hardware" || key == "system") {
		key += " " + n.arg(1)
	}
	p.cfg.Unparsed[key]++
}

// top dispatches one top-level block.
func (p *configParser) top(n *node) {
	switch {
	case n.has("version"):
		p.cfg.Version = n.arg(1)
	case n.has("hostname") || n.has("switchname"):
		p.cfg.Hostname = n.arg(1)
	case n.has("feature"):
		p.cfg.Features[n.rest(1)] = true
	case n.has("nv", "overlay", "evpn"):
		p.cfg.Features["nv overlay evpn"] = true
	case n.has("fabric", "forwarding", "anycast-gateway-mac"):
		p.anycastMAC(n)
	case n.has("system", "nve", "infra-vlans"):
		p.infraVLANs(n)
	case n.has("hardware", "access-list", "tcam", "region"):
		size, err := strconv.Atoi(n.arg(5))
		if err == nil {
			p.cfg.TCAMRegions[n.arg(4)] = size
		}
	case n.has("vlan"):
		p.vlan(n)
	case n.has("vrf", "context"):
		p.vrf(n)
	case n.has("interface"):
		p.iface(n)
	case n.has("evpn"):
		p.evpn(n)
	case n.has("router", "bgp"):
		p.bgp(n)
	case n.has("router", "ospf"):
		p.ospf(n)
	case n.has("vpc", "domain"):
		p.vpc(n)
	case n.has("ip", "access-list"):
		p.cfg.ACLs[n.arg(2)] += len(n.children)
	case n.has("ip", "prefix-list"):
		p.cfg.PrefixLists[n.arg(2)]++
	case n.has("route-map"):
		p.cfg.RouteMaps[n.arg(1)]++
	case n.has("ip", "route"):
		p.staticRoute("default", n)
	default:
		if !p.management(n) {
			p.unparsed(n)
		}
	}
}

func (p *configParser) finish() {
	for _, list := range p.vlanList {
		ids, err := parseVLANList(list)
		if err != nil {
			continue
		}
		for _, v := range ids {
			if _, ok := p.cfg.VLANs[v]; !ok {
				p.cfg.VLANs[v] = device.VLAN{}
			}
		}
	}
}

func atoi(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}

// digest returns a short, non-reversible tag for a secret so that drift in
// shared keys is detectable without storing them.
func digest(secret string) string {
	return evidence.Digest([]byte(secret))[:16]
}

// after returns the word following key in fields, or "".
func after(fields []string, key string) string {
	for i := 0; i+1 < len(fields); i++ {
		if fields[i] == key {
			return fields[i+1]
		}
	}
	return ""
}

func contains(fields []string, word string) bool { return slices.Contains(fields, word) }

func joinFrom(fields []string, i int) string {
	if i >= len(fields) {
		return ""
	}
	return strings.Join(fields[i:], " ")
}
