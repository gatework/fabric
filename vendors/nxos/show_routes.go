package nxos

import (
	"regexp"
	"strings"

	"github.com/gatework/go-netops/textscan"

	"github.com/gatework/fabric"
	"github.com/gatework/fabric/device"
)

var (
	macToken   = regexp.MustCompile(`^[0-9a-f]{4}\.[0-9a-f]{4}\.[0-9a-f]{4}$`)
	labelToken = regexp.MustCompile(`\(Label:\s*\d+\)`)
)

// nextHops splits "1.1.1.1 (Label: 10090)" or "1.1.1.1,2.2.2.2" into hops;
// labels are dropped.
func nextHops(text string) []string {
	text = labelToken.ReplaceAllString(text, "")
	var out []string
	for _, h := range strings.FieldsFunc(text, func(r rune) bool { return r == ',' || r == ' ' }) {
		if h != "" {
			out = append(out, h)
		}
	}
	if out == nil {
		out = []string{}
	}
	return out
}

func parseL2Routes(st *device.State, text string) *result {
	r := &result{empty: blank(text)}
	st.L2Routes = []device.L2Route{}
	legend := true
	for line := range textscan.Lines(text) {
		t := strings.TrimSpace(line.Text)
		f := strings.Fields(t)
		switch {
		case t == "" || dashes(t):
		case strings.HasPrefix(t, "Topology"):
			r.header, legend = true, false
		case legend:
		case len(f) >= 6 && macToken.MatchString(f[1]):
			st.L2Routes = append(st.L2Routes, device.L2Route{
				Topology: f[0], MAC: fabric.MAC(f[1]), Producer: f[2], Flags: f[3],
				Sequence: max(atoiStrict(f[4]), 0), NextHops: nextHops(strings.Join(f[5:], " ")),
			})
		default:
			r.scan.Unparsed(line.Number)
		}
	}
	return r
}

// parseMACIPs reads both column orders: newer releases put "Host IP" after
// the MAC, 7.0(3)I7 puts it after "Seq No". The header decides.
func parseMACIPs(st *device.State, text string) *result {
	r := &result{empty: blank(text)}
	st.MACIPs = []device.MACIPRoute{}
	legend, ipFirst := true, true
	for line := range textscan.Lines(text) {
		t := strings.TrimSpace(line.Text)
		f := strings.Fields(t)
		switch {
		case t == "" || dashes(t):
		case strings.HasPrefix(t, "Topology"):
			r.header, legend = true, false
			ipFirst = strings.Index(t, "Host IP") < strings.Index(t, "Prod")
		case legend:
		case len(f) >= 7 && macToken.MatchString(f[1]):
			e := device.MACIPRoute{Topology: f[0], MAC: fabric.MAC(f[1])}
			if ipFirst {
				e.IP, e.Producer, e.Flags, e.Sequence = f[2], f[3], f[4], max(atoiStrict(f[5]), 0)
			} else {
				e.Producer, e.Flags, e.Sequence, e.IP = f[2], f[3], max(atoiStrict(f[4]), 0), f[5]
			}
			e.NextHops = nextHops(strings.Join(f[6:], " "))
			st.MACIPs = append(st.MACIPs, e)
		default:
			r.scan.Unparsed(line.Number)
		}
	}
	return r
}

func parseCDP(st *device.State, text string) *result {
	r := &result{empty: blank(text)}
	st.Neighbors = []device.CDPNeighbor{}
	var cur *device.CDPNeighbor
	inMgmt := false
	flush := func() {
		if cur != nil && cur.DeviceID != "" {
			st.Neighbors = append(st.Neighbors, *cur)
		}
		cur = nil
	}
	for line := range textscan.Lines(text) {
		t := strings.TrimSpace(line.Text)
		switch {
		case strings.HasPrefix(t, "Device ID:"):
			flush()
			r.header = true
			cur = &device.CDPNeighbor{DeviceID: strings.TrimSpace(strings.TrimPrefix(t, "Device ID:"))}
			inMgmt = false
		case cur == nil:
		case strings.HasPrefix(t, "System Name:"):
			cur.SystemName = strings.TrimSpace(strings.TrimPrefix(t, "System Name:"))
		case strings.HasPrefix(t, "Platform:"):
			p, _, _ := strings.Cut(strings.TrimPrefix(t, "Platform:"), ",")
			cur.Platform = strings.TrimSpace(p)
		case strings.HasPrefix(t, "Interface:"):
			local, remote, _ := strings.Cut(strings.TrimPrefix(t, "Interface:"), ",")
			cur.LocalPort, _ = device.CanonicalInterface(local)
			_, port, _ := strings.Cut(remote, ":")
			cur.RemotePort = strings.TrimSpace(port)
		case strings.HasPrefix(t, "Mgmt address"):
			inMgmt = true
		case strings.HasPrefix(t, "IPv4 Address:"):
			addr := strings.TrimSpace(strings.TrimPrefix(t, "IPv4 Address:"))
			if inMgmt {
				cur.MgmtAddress = addr
			} else if cur.Address == "" {
				cur.Address = addr
			}
		}
	}
	flush()
	return r
}
