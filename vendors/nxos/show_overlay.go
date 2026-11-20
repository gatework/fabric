package nxos

import (
	"regexp"
	"strings"

	"github.com/gatework/netops/parse"

	"github.com/gatework/fabric"
	"github.com/gatework/fabric/device"
)

func parseNVEPeers(st *device.State, text string) *result {
	r := &result{empty: blank(text)}
	st.NVEPeers = []device.NVEPeer{}
	for line := range parse.Lines(text) {
		t := strings.TrimSpace(line.Text)
		f := strings.Fields(t)
		switch {
		case t == "" || dashes(t):
		case strings.HasPrefix(t, "Interface") && strings.Contains(t, "Peer-IP"):
			r.header = true
		case r.header && len(f) >= 3 && strings.HasPrefix(f[0], "nve"):
			p := device.NVEPeer{Interface: f[0], Peer: f[1], State: f[2], Up: f[2] == "Up"}
			if len(f) > 3 {
				p.LearnType = f[3]
			}
			if len(f) > 4 {
				p.Uptime = f[4]
			}
			if len(f) > 5 {
				p.RouterMAC, _ = fabric.ParseMAC(f[5])
			}
			st.NVEPeers = append(st.NVEPeers, p)
		default:
			r.scan.Issue(parse.IssueUnparsed, line.Number)
		}
	}
	// Spines with NVE enabled but no peers print only the header.
	return r
}

var nveVNIRow = regexp.MustCompile(`^(nve\d+)\s+(\d+)\s+(\S+)\s+(\S+)\s+(\S+)\s+(L[23])\s+\[([^\]]*)\]\s*(\S*)`)

func parseNVEVNIs(st *device.State, text string) *result {
	r := &result{empty: blank(text)}
	st.NVEVNIs = []device.NVEVNI{}
	legend := true
	for line := range parse.Lines(text) {
		t := strings.TrimSpace(line.Text)
		switch {
		case t == "" || dashes(t):
		case strings.HasPrefix(t, "Interface") && strings.Contains(t, "VNI"):
			r.header, legend = true, false
		case legend:
			// Codes legend before the header.
		case r.header:
			m := nveVNIRow.FindStringSubmatch(t)
			if m == nil {
				r.scan.Issue(parse.IssueUnparsed, line.Number)
				continue
			}
			vni, err := fabric.ParseVNI(m[2])
			if err != nil {
				r.scan.Issue(parse.IssueUnparsed, line.Number)
				continue
			}
			st.NVEVNIs = append(st.NVEVNIs, device.NVEVNI{
				Interface: m[1], VNI: vni, McastGroup: m[3], State: m[4], Up: m[4] == "Up",
				Mode: m[5], Layer: m[6], Domain: m[7], Flags: m[8],
			})
		default:
			r.scan.Issue(parse.IssueUnparsed, line.Number)
		}
	}
	return r
}

var (
	bgpRouterID = regexp.MustCompile(`router identifier (\S+), local AS number (\S+)`)
	bgpPeers    = regexp.MustCompile(`config peers (\d+), capable peers (\d+)`)
)

func parseBGPSummary(st *device.State, text string) *result {
	r := &result{empty: blank(text)}
	st.BGP = device.BGPSummary{Neighbors: []device.BGPNeighbor{}}
	table := ""
	for line := range parse.Lines(text) {
		t := strings.TrimSpace(line.Text)
		f := strings.Fields(t)
		switch {
		case t == "":
			table = ""
		case bgpRouterID.MatchString(t):
			m := bgpRouterID.FindStringSubmatch(t)
			st.BGP.RouterID = m[1]
			st.BGP.LocalAS, _ = fabric.ParseASN(m[2])
		case bgpPeers.MatchString(t):
			m := bgpPeers.FindStringSubmatch(t)
			st.BGP.ConfigPeers, st.BGP.CapablePeers = atoi(m[1]), atoi(m[2])
			r.header = true
		case strings.HasPrefix(t, "Neighbor") && strings.Contains(t, "State/PfxRcd"):
			table, r.header = "sessions", true
		case strings.HasPrefix(t, "Neighbor") && strings.Contains(t, "Type-2"):
			table = "types"
		case strings.HasPrefix(t, "BGP ") || strings.Contains(t, "network entries"):
		case table == "sessions" && len(f) >= 10:
			st.BGP.Neighbors = append(st.BGP.Neighbors, bgpNeighbor(f))
		case table == "types":
			// The per-route-type breakdown repeats the prefix count.
		default:
			r.scan.Issue(parse.IssueUnparsed, line.Number)
		}
	}
	// Dynamic (prefix) neighbors make "config peers" exceed the listed
	// sessions, but every capable peer must be listed.
	if len(st.BGP.Neighbors) < st.BGP.CapablePeers {
		r.scan.Issue(IssueCountMismatch, 0)
	}
	return r
}

func bgpNeighbor(f []string) device.BGPNeighbor {
	n := device.BGPNeighbor{Address: f[0], UpDown: f[8]}
	n.AS, _ = fabric.ParseASN(f[2])
	last := strings.Join(f[9:], " ")
	if pfx := atoiStrict(last); pfx >= 0 {
		n.Established, n.Prefixes = true, pfx
	} else {
		n.State = last
	}
	return n
}

// atoiStrict returns the number or -1.
func atoiStrict(s string) int {
	if s == "" {
		return -1
	}
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return -1
		}
		n = n*10 + int(c-'0')
	}
	return n
}

var (
	ospfProcess = regexp.MustCompile(`^OSPF Process ID (\S+) VRF (\S+)`)
	ospfRow     = regexp.MustCompile(`^(\S+)\s+(\d+)\s+(\S+(?:/\s*\S+)?)\s+(\S+)\s+(\S+)\s+(\S+)$`)
)

func parseOSPF(st *device.State, text string) *result {
	r := &result{empty: blank(text)}
	st.OSPF = []device.OSPFNeighbor{}
	process, vrf := "", ""
	for line := range parse.Lines(text) {
		t := strings.TrimSpace(line.Text)
		switch {
		case t == "":
		case ospfProcess.MatchString(t):
			m := ospfProcess.FindStringSubmatch(t)
			process, vrf = m[1], m[2]
			r.header = true
		case strings.HasPrefix(t, "Total number of neighbors"), strings.HasPrefix(t, "Neighbor ID"):
		case process != "" && ospfRow.MatchString(t):
			m := ospfRow.FindStringSubmatch(t)
			name, _ := device.CanonicalInterface(m[6])
			st.OSPF = append(st.OSPF, device.OSPFNeighbor{
				Process: process, VRF: vrf, RouterID: m[1], State: m[3],
				Full: strings.HasPrefix(m[3], "FULL"), Address: m[5], Interface: name,
			})
		default:
			r.scan.Issue(parse.IssueUnparsed, line.Number)
		}
	}
	return r
}
