package nxos

import (
	"regexp"
	"strings"

	"github.com/gatework/netops/parse"

	"github.com/gatework/fabric"
	"github.com/gatework/fabric/device"
)

var vpcField = regexp.MustCompile(`^([A-Za-z][\w ./()-]*?)\s*:\s*(.*)$`)

func parseVPC(st *device.State, text string) *result {
	r := &result{empty: blank(text)}
	v := &device.VPCStatus{PeerLinks: []device.VPCLink{}, Members: []device.VPCMember{}}
	section := "head"
	var cols columns
	for line := range parse.Lines(text) {
		raw := strings.TrimRight(line.Text, " ")
		t := strings.TrimSpace(raw)
		switch {
		case section == "note":
		case t == "" || dashes(t) || t == "Legend:" || strings.HasPrefix(t, "(*)"):
		case t == "vPC Peer-link status":
			section = "peer"
		case t == "vPC status":
			section = "members"
		case strings.HasPrefix(t, "Please check"):
			section = "note"
		case section == "head" && vpcField.MatchString(t):
			m := vpcField.FindStringSubmatch(t)
			vpcHead(v, strings.ToLower(m[1]), strings.TrimSpace(m[2]))
			r.header = true
		case section == "peer" && strings.HasPrefix(t, "id"):
			cols, _ = newColumns(raw, "id", "Port", "Status", "Active vlans")
		case section == "members" && strings.HasPrefix(t, "Id"):
			cols, _ = newColumns(raw, "Id", "Port", "Status", "Consistency", "Reason", "Active vlans")
		case section == "peer" && len(cols.starts) == 4:
			vpcPeerRow(v, cols.cells(raw))
		case section == "members" && len(cols.starts) == 6:
			if !vpcMemberRow(v, cols.cells(raw)) {
				r.scan.Issue(parse.IssueUnparsed, line.Number)
			}
		default:
			r.scan.Issue(parse.IssueUnparsed, line.Number)
		}
	}
	if r.header {
		st.VPC = v
	}
	return r
}

func vpcPeerRow(v *device.VPCStatus, c []string) {
	if c[0] == "" && len(v.PeerLinks) > 0 {
		v.PeerLinks[len(v.PeerLinks)-1].VLANs += c[3]
		return
	}
	l := device.VPCLink{ID: atoi(c[0]), Up: c[2] == "up", VLANs: c[3]}
	l.Port, _ = device.CanonicalInterface(c[1])
	v.PeerLinks = append(v.PeerLinks, l)
}

func vpcMemberRow(v *device.VPCStatus, c []string) bool {
	if c[0] == "" {
		if len(v.Members) == 0 {
			return false
		}
		last := &v.Members[len(v.Members)-1]
		last.Reason = strings.TrimSpace(last.Reason + " " + c[4])
		last.VLANs += c[5]
		if c[3] != "" {
			last.Consistent = false
		}
		return true
	}
	if atoiStrict(c[0]) < 0 {
		return false
	}
	m := device.VPCMember{ID: atoi(c[0]), Up: c[2] == "up", Consistent: c[3] == "success", Reason: c[4], VLANs: c[5]}
	m.Port, _ = device.CanonicalInterface(c[1])
	v.Members = append(v.Members, m)
	return true
}

func vpcHead(v *device.VPCStatus, key, val string) {
	lower := strings.ToLower(val)
	switch key {
	case "vpc domain id":
		v.DomainID = atoi(strings.Fields(val + " 0")[0])
	case "peer status":
		v.PeerStatus, v.PeerOK = val, strings.Contains(lower, "formed ok")
	case "vpc keep-alive status":
		v.KeepaliveStatus, v.KeepaliveOK = val, strings.Contains(lower, "peer is alive")
	case "configuration consistency status":
		v.ConsistencyOK = lower == "success"
	case "per-vlan consistency status":
		v.PerVLANConsistent = lower == "success"
	case "type-2 consistency status":
		v.Type2Consistent = lower == "success"
	case "vpc role":
		v.Role = val
	case "virtual-peerlink mode":
		v.VirtualPeerLink = lower == "enabled"
	}
}

func parseInterfaces(st *device.State, text string) *result {
	r := &result{empty: blank(text)}
	st.Interfaces = []device.InterfaceStatus{}
	for line := range parse.Lines(text) {
		t := strings.TrimSpace(line.Text)
		f := strings.Fields(t)
		if t == "" || dashes(t) {
			continue
		}
		name, kind := device.CanonicalInterface(f[0])
		if kind == device.KindOther {
			if isHeaderWord(f[0]) {
				r.header = true
				continue
			}
			r.scan.Issue(parse.IssueUnparsed, line.Number)
			continue
		}
		s, ok := interfaceRow(name, kind, f)
		if !ok {
			r.scan.Issue(parse.IssueUnparsed, line.Number)
			continue
		}
		st.Interfaces = append(st.Interfaces, s)
	}
	return r
}

func isHeaderWord(w string) bool {
	switch w {
	case "Port", "Ethernet", "Interface", "Port-channel":
		return true
	default:
		return false
	}
}

// interfaceRow reads one row; the status column moves with the section.
func interfaceRow(name string, kind device.InterfaceKind, f []string) (device.InterfaceStatus, bool) {
	at := map[device.InterfaceKind]int{
		device.KindEthernet: 4, device.KindPortChannel: 4, device.KindMgmt: 2,
		device.KindLoopback: 1, device.KindSVI: 2, device.KindNVE: 1,
	}[kind]
	if at >= len(f) {
		return device.InterfaceStatus{}, false
	}
	s := device.InterfaceStatus{Name: name, Status: f[at], Up: f[at] == "up"}
	switch kind {
	case device.KindEthernet, device.KindPortChannel:
		if len(f) < 8 {
			return s, false
		}
		s.VLAN, s.Mode = f[1], f[3]
		s.Reason = strings.Join(f[5:len(f)-2], " ")
		s.Speed = f[len(f)-2]
		if kind == device.KindEthernet {
			s.PortChannel = max(atoiStrict(f[len(f)-1]), 0)
		}
	case device.KindSVI, device.KindNVE:
		s.Reason = strings.Join(f[at+1:], " ")
	case device.KindMgmt, device.KindLoopback, device.KindOther:
	}
	return s, true
}

var poRow = regexp.MustCompile(`^(\d+)\s+(Po\d+)\(([A-Za-z]+)\)\s+(\S+)\s+(\S+)\s*(.*)$`)
var poMember = regexp.MustCompile(`^(\S+)\(([A-Za-z]+)\)$`)

func parsePortChannels(st *device.State, text string) *result {
	r := &result{empty: blank(text)}
	st.PortChannels = []device.PortChannel{}
	legend := true
	for line := range parse.Lines(text) {
		t := strings.TrimSpace(line.Text)
		switch {
		case t == "" || dashes(t):
		case strings.HasPrefix(t, "Group"):
			r.header, legend = true, false
		case legend || t == "Channel":
		case poRow.MatchString(t):
			m := poRow.FindStringSubmatch(t)
			pc := device.PortChannel{Group: atoi(m[1]), Flags: m[3], Up: strings.Contains(m[3], "U"), Protocol: m[5]}
			pc.Name, _ = device.CanonicalInterface(m[2])
			pc.Members = addMembers(nil, m[6])
			st.PortChannels = append(st.PortChannels, pc)
		case len(st.PortChannels) > 0 && poMember.MatchString(strings.Fields(t)[0]):
			last := &st.PortChannels[len(st.PortChannels)-1]
			last.Members = addMembers(last.Members, t)
		default:
			r.scan.Issue(parse.IssueUnparsed, line.Number)
		}
	}
	return r
}

func addMembers(ms []device.PortChannelMember, text string) []device.PortChannelMember {
	if ms == nil {
		ms = []device.PortChannelMember{}
	}
	for tok := range strings.FieldsSeq(text) {
		m := poMember.FindStringSubmatch(tok)
		if m == nil {
			continue
		}
		port, _ := device.CanonicalInterface(m[1])
		ms = append(ms, device.PortChannelMember{Port: port, Flag: m[2], Bundled: m[2] == "P"})
	}
	return ms
}

func parseMACs(st *device.State, text string) *result {
	r := &result{empty: blank(text)}
	st.MACs = make([]device.MACEntry, 0, strings.Count(text, "\n"))
	legend := true
	var tok tokens
	for line := range parse.Lines(text) {
		t := strings.TrimSpace(line.Text)
		switch {
		case t == "" || dashes(t):
		case strings.HasPrefix(t, "VLAN") && strings.Contains(t, "MAC Address"):
			r.header, legend = true, false
		case legend:
		default:
			e, ok := macRow(tok.split(t))
			if !ok {
				r.scan.Issue(parse.IssueUnparsed, line.Number)
				continue
			}
			st.MACs = append(st.MACs, e)
		}
	}
	return r
}

// macRow reads "[marker] VLAN MAC type age secure ntfy port...".
func macRow(f []string) (device.MACEntry, bool) {
	var e device.MACEntry
	if len(f) > 0 && isMACMarker(f[0]) {
		e.Marker, f = f[0], f[1:]
	}
	if len(f) < 7 || (!isDigits(f[0]) && f[0] != "-") || !isMAC(f[1]) {
		return e, false
	}
	e.MAC, e.Type = fabric.MAC(f[1]), f[2]
	if v, err := fabric.ParseVLAN(f[0]); err == nil {
		e.VLAN = v
	}
	port := f[6]
	if len(f) > 7 {
		port = strings.Join(f[6:], " ")
	}
	switch {
	case strings.HasPrefix(port, "nve") && strings.HasSuffix(port, ")"):
		if open := strings.IndexByte(port, '('); open > 0 {
			e.RemoteVTEP = port[open+1 : len(port)-1]
		}
	case !strings.Contains(port, " "):
		port, _ = device.CanonicalInterface(port)
	}
	e.Port = port
	return e, true
}
