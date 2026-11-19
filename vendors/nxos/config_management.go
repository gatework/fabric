package nxos

import (
	"strconv"

	"github.com/gatework/fabric/device"
)

// defaultLogSeverity is the NX-OS severity of a logging server
// configured without one.
const defaultLogSeverity = 5

// management parses management-plane lines and reports whether it did.
func (p *configParser) management(n *node) bool {
	m := &p.cfg.Management
	switch {
	case n.has("clock", "timezone"):
		m.Timezone = n.rest(2)
	case n.has("ip", "domain-name"):
		m.DomainName = n.arg(2)
	case n.has("no", "ip", "domain-lookup"):
		m.DomainLookup = false
	case n.has("ip", "domain-lookup"):
		m.DomainLookup = true
	case n.has("ntp"):
		p.ntp(n)
	case n.has("logging"):
		return p.logging(n)
	case n.has("tacacs-server", "host"):
		m.TACACS.Servers = append(m.TACACS.Servers, device.TACACSServer{
			Address: n.arg(2), KeyDigest: secretAfter(n.fields, "key"),
		})
	case n.has("ip", "tacacs", "source-interface"):
		m.TACACS.SourceInterface, _ = device.CanonicalInterface(n.arg(3))
	case n.has("aaa", "group", "server", "tacacs+"):
		m.TACACS.Groups = append(m.TACACS.Groups, n.arg(4))
	case n.has("aaa"):
		m.AAA = append(m.AAA, n.text)
	case n.has("snmp-server"):
		return p.snmp(n)
	case n.has("line", "vty"):
		for _, c := range n.children {
			switch {
			case c.has("exec-timeout"):
				m.VTYExecTimeout = atoi(c.arg(1))
			case c.has("session-limit"):
				m.VTYSessionLimit = atoi(c.arg(1))
			}
		}
	case n.has("line", "console"):
		for _, c := range n.children {
			if c.has("exec-timeout") {
				m.ConsoleExecTimeout = atoi(c.arg(1))
			}
		}
	case n.has("copp", "profile"):
		m.CoPPProfile = n.arg(2)
	case n.has("username"), n.has("tacacs-server", "key"), n.has("no", "password"):
		// Credentials are never modeled.
	default:
		return false
	}
	return true
}

// secretAfter digests the secret that follows key, skipping an optional
// encryption type number and surrounding quotes.
func secretAfter(fields []string, key string) string {
	for i := 0; i+1 < len(fields); i++ {
		if fields[i] != key {
			continue
		}
		j := i + 1
		if _, err := strconv.Atoi(fields[j]); err == nil && j+1 < len(fields) {
			j++
		}
		return digest(fields[j])
	}
	return ""
}

func (p *configParser) ntp(n *node) {
	t := &p.cfg.Management.NTP
	switch {
	case n.has("ntp", "server"):
		t.Servers = append(t.Servers, device.NTPServer{
			Address: n.arg(2),
			VRF:     after(n.fields, "use-vrf"),
			Prefer:  contains(n.fields, "prefer"),
		})
	case n.has("ntp", "source-interface"):
		t.SourceInterface, _ = device.CanonicalInterface(n.arg(2))
	case n.has("ntp", "logging"):
		t.Logging = true
	case n.has("ntp", "master"):
		t.Master = true
	}
}

func (p *configParser) logging(n *node) bool {
	l := &p.cfg.Management.Logging
	switch {
	case n.has("logging", "server"):
		s := device.LogServer{
			Address:  n.arg(2),
			VRF:      after(n.fields, "use-vrf"),
			Facility: after(n.fields, "facility"),
			Port:     atoi(after(n.fields, "port")),
		}
		// Releases before 9.x omit the default severity (5) that later
		// releases print, so it is made explicit here.
		s.Severity = defaultLogSeverity
		if sev, err := strconv.Atoi(n.arg(3)); err == nil {
			s.Severity = sev
		}
		l.Servers = append(l.Servers, s)
	case n.has("logging", "source-interface"):
		l.SourceInterface, _ = device.CanonicalInterface(n.arg(2))
	case n.has("logging", "origin-id", "hostname"):
		l.OriginIDHostname = true
	case n.has("logging", "timestamp"):
		l.TimestampUnit = n.arg(2)
	case n.has("logging", "level"), n.has("logging", "logfile"), n.has("logging", "monitor"),
		n.has("logging", "event"), n.has("logging", "console"):
		// Local log levels are platform tuning, not fabric state.
	default:
		return false
	}
	return true
}

func (p *configParser) snmp(n *node) bool {
	s := &p.cfg.Management.SNMP
	switch {
	case n.has("snmp-server", "contact"):
		s.Contact = n.rest(2)
	case n.has("snmp-server", "location"):
		s.Location = n.rest(2)
	case n.has("snmp-server", "community"):
		s.Communities = append(s.Communities, device.SNMPCommunity{
			Digest: digest(n.arg(2)),
			Group:  after(n.fields, "group"),
			ACL:    after(n.fields, "use-ipv4acl"),
		})
	case n.has("snmp-server", "user"):
		u := device.SNMPUser{Name: n.arg(2), Auth: after(n.fields, "auth")}
		if contains(n.fields, "priv") {
			u.Priv = "des"
			if contains(n.fields, "aes-128") {
				u.Priv = "aes-128"
			}
		}
		s.Users = append(s.Users, u)
	case n.has("snmp-server", "host"):
		s.Hosts = append(s.Hosts, n.arg(2))
	case n.has("snmp-server", "enable"):
		// Trap enables are platform defaults.
	default:
		return false
	}
	return true
}
