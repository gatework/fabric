package device

import (
	"slices"
)

// Management is the switch's management plane: time, logging, AAA, SNMP
// and terminal lines. Secrets are kept only as digests.
type Management struct {
	Timezone           string   `json:"timezone,omitempty"`
	DomainName         string   `json:"domain_name,omitempty"`
	DomainLookup       bool     `json:"domain_lookup"`
	NTP                NTP      `json:"ntp"`
	Logging            Logging  `json:"logging"`
	TACACS             TACACS   `json:"tacacs"`
	AAA                []string `json:"aaa,omitempty"`
	SNMP               SNMP     `json:"snmp"`
	VTYExecTimeout     int      `json:"vty_exec_timeout,omitempty"`
	VTYSessionLimit    int      `json:"vty_session_limit,omitempty"`
	ConsoleExecTimeout int      `json:"console_exec_timeout,omitempty"`
	CoPPProfile        string   `json:"copp_profile,omitempty"`
}

func (m Management) clone() Management {
	m.NTP.Servers = slices.Clone(m.NTP.Servers)
	m.Logging.Servers = slices.Clone(m.Logging.Servers)
	m.TACACS.Servers = slices.Clone(m.TACACS.Servers)
	m.TACACS.Groups = slices.Clone(m.TACACS.Groups)
	m.AAA = slices.Clone(m.AAA)
	m.SNMP.Communities = slices.Clone(m.SNMP.Communities)
	m.SNMP.Users = slices.Clone(m.SNMP.Users)
	m.SNMP.Hosts = slices.Clone(m.SNMP.Hosts)
	return m
}

// NTP is the time configuration.
type NTP struct {
	Servers         []NTPServer `json:"servers,omitempty"`
	SourceInterface string      `json:"source_interface,omitempty"`
	Logging         bool        `json:"logging,omitempty"`
	Master          bool        `json:"master,omitempty"`
}

// NTPServer is one "ntp server".
type NTPServer struct {
	Address string `json:"address" yaml:"address"`
	VRF     string `json:"vrf,omitempty" yaml:"vrf,omitempty"`
	Prefer  bool   `json:"prefer,omitempty" yaml:"prefer,omitempty"`
}

// Server returns the server with address.
func (n NTP) Server(addr string) (NTPServer, bool) {
	i := slices.IndexFunc(n.Servers, func(s NTPServer) bool { return s.Address == addr })
	if i < 0 {
		return NTPServer{}, false
	}
	return n.Servers[i], true
}

// Logging is the syslog configuration.
type Logging struct {
	Servers          []LogServer `json:"servers,omitempty"`
	SourceInterface  string      `json:"source_interface,omitempty"`
	OriginIDHostname bool        `json:"origin_id_hostname,omitempty"`
	TimestampUnit    string      `json:"timestamp_unit,omitempty"`
}

// LogServer is one "logging server".
type LogServer struct {
	Address  string `json:"address" yaml:"address"`
	Severity int    `json:"severity,omitempty" yaml:"severity,omitempty"`
	Port     int    `json:"port,omitempty" yaml:"port,omitempty"`
	VRF      string `json:"vrf,omitempty" yaml:"vrf,omitempty"`
	Facility string `json:"facility,omitempty" yaml:"facility,omitempty"`
}

// Server returns the server with address.
func (l Logging) Server(addr string) (LogServer, bool) {
	i := slices.IndexFunc(l.Servers, func(s LogServer) bool { return s.Address == addr })
	if i < 0 {
		return LogServer{}, false
	}
	return l.Servers[i], true
}

// TACACS is the TACACS+ configuration.
type TACACS struct {
	Servers         []TACACSServer `json:"servers,omitempty"`
	Groups          []string       `json:"groups,omitempty"`
	SourceInterface string         `json:"source_interface,omitempty"`
}

// TACACSServer is one "tacacs-server host"; the key is a digest.
type TACACSServer struct {
	Address   string `json:"address"`
	KeyDigest string `json:"key_digest,omitempty"`
}

// SNMP is the SNMP agent configuration.
type SNMP struct {
	Contact     string          `json:"contact,omitempty"`
	Location    string          `json:"location,omitempty"`
	Communities []SNMPCommunity `json:"communities,omitempty"`
	Users       []SNMPUser      `json:"users,omitempty"`
	Hosts       []string        `json:"hosts,omitempty"`
}

// SNMPCommunity is one community; its name is a digest because it is a
// shared secret.
type SNMPCommunity struct {
	Digest string `json:"digest"`
	Group  string `json:"group,omitempty"`
	ACL    string `json:"acl,omitempty"`
}

// SNMPUser is one local SNMPv3 user.
type SNMPUser struct {
	Name string `json:"name"`
	Auth string `json:"auth,omitempty"`
	Priv string `json:"priv,omitempty"`
}
