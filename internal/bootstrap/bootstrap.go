package bootstrap

import (
	"errors"
	"fmt"
	"net/netip"
	"os"
	"time"

	"github.com/gatework/net-connector/connector"
	"github.com/gatework/net-connector/transport/ssh"
	"github.com/gatework/net-connector/vendors"
	"github.com/gatework/netops/assess"
	"github.com/gatework/netops/authz"
	nbootstrap "github.com/gatework/netops/bootstrap"
	"github.com/gatework/netops/credential"
	ndevice "github.com/gatework/netops/device"
	"github.com/gatework/netops/lease"
	"github.com/gatework/netops/secret"
	filelease "github.com/gatework/netops/store/lease"

	"github.com/gatework/fabric"
	"github.com/gatework/fabric/adapter/netconnector"
	"github.com/gatework/fabric/baseline"
	"github.com/gatework/fabric/internal/workflow"
	"github.com/gatework/fabric/vendors/nxos"
)

// ErrCredentials reports missing device credentials.
var ErrCredentials = errors.New("bootstrap: device credentials missing; set NC_DEVICE_USERNAME and NC_DEVICE_PASSWORD")

// maxFile bounds every document read from disk.
const maxFile = 16 << 20

func readFile(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("bootstrap: %w", err)
	}
	if info.Size() > maxFile {
		return nil, fmt.Errorf("bootstrap: %s exceeds %d bytes", path, maxFile)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("bootstrap: %w", err)
	}
	return body, nil
}

// ReadIntent reads and validates an intent file; an empty path means none.
func ReadIntent(path string) (*fabric.Intent, error) {
	if path == "" {
		return nil, nil //nolint:nilnil // no intent is a valid, explicit choice.
	}
	body, err := readFile(path)
	if err != nil {
		return nil, err
	}
	return fabric.DecodeIntent(body)
}

// ReadBaseline reads a baseline profile; an empty path means none.
func ReadBaseline(path string) (*baseline.Profile, error) {
	if path == "" {
		return nil, nil //nolint:nilnil // no baseline is a valid, explicit choice.
	}
	body, err := readFile(path)
	if err != nil {
		return nil, err
	}
	return baseline.Decode(body)
}

// ReadPolicy reads a rule policy; an empty path means the defaults.
func ReadPolicy(path string) (assess.Policy, error) {
	if path == "" {
		return assess.Policy{}, nil
	}
	body, err := readFile(path)
	if err != nil {
		return assess.Policy{}, err
	}
	p, err := assess.DecodePolicy(body)
	if err != nil {
		return assess.Policy{}, fmt.Errorf("bootstrap: %w", err)
	}
	return p, nil
}

// SSH holds connection settings shared by every switch.
type SSH struct {
	Port           int
	KnownHosts     string
	HostKeyPolicy  string
	LoginTimeout   time.Duration
	CommandTimeout time.Duration
}

// Credentials are the device login, read from the environment.
type Credentials struct {
	Username string
	Password string
	Enable   string
}

// CredentialsFromEnv reads NC_CISCO_NXOS_USERNAME/PASSWORD, falling back
// to NC_DEVICE_USERNAME/PASSWORD, and NC_ENABLE_PASSWORD.
func CredentialsFromEnv(lookup func(string) (string, bool)) (Credentials, error) {
	get := func(keys ...string) string {
		for _, k := range keys {
			if v, ok := lookup(k); ok && v != "" {
				return v
			}
		}
		return ""
	}
	c := Credentials{
		Username: get("NC_CISCO_NXOS_USERNAME", "NC_DEVICE_USERNAME"),
		Password: get("NC_CISCO_NXOS_PASSWORD", "NC_DEVICE_PASSWORD"),
		Enable:   get("NC_ENABLE_PASSWORD"),
	}
	if c.Username == "" || c.Password == "" {
		return c, ErrCredentials
	}
	return c, nil
}

// Switch is the netops identity of the NX-OS switch at addr.
func Switch(addr netip.Addr) ndevice.Device {
	return ndevice.Device{ID: addr.String(), Mgmt: addr, Platform: nxos.Platform}
}

// Dialer returns the deployment dialer.
func (s SSH) Dialer(c Credentials) (workflow.Dialer, error) {
	d, err := s.connector(c)
	if err != nil {
		return nil, err
	}
	return func(addr netip.Addr) workflow.Switch { return netconnector.New(d, Switch(addr)) }, nil
}

// Collector returns the collection dialer.
func (s SSH) Collector(c Credentials) (func(netip.Addr) workflow.Collector, error) {
	d, err := s.connector(c)
	if err != nil {
		return nil, err
	}
	return func(addr netip.Addr) workflow.Collector { return netconnector.New(d, Switch(addr)) }, nil
}

// connector assembles the net-connector dialer: SSH with the host key
// policy, the built-in NX-OS driver and the environment credentials.
func (s SSH) connector(c Credentials) (*connector.Dialer, error) {
	sd, err := ssh.New(ssh.Options{KnownHosts: s.KnownHosts, HostKeys: ssh.Policy(s.HostKeyPolicy), Timeout: s.LoginTimeout})
	if err != nil {
		return nil, fmt.Errorf("bootstrap: ssh: %w", err)
	}
	d, err := connector.NewDialer(connector.Options{
		Registry: vendors.Builtin(),
		Credentials: credential.Static{
			Username: c.Username, Password: secret.New(c.Password), EnablePassword: secret.New(c.Enable),
		},
		Transport: sd,
		Port:      s.Port,
		Timeout:   s.CommandTimeout,
	})
	if err != nil {
		return nil, fmt.Errorf("bootstrap: %w", err)
	}
	return d, nil
}

// ReadAuthz reads an authorization policy. An empty path means no policy:
// the operator running fabric is trusted, as before.
func ReadAuthz(path string) (*authz.Policy, error) {
	if path == "" {
		return nil, nil //nolint:nilnil // no policy is a valid, explicit choice.
	}
	body, err := readFile(path)
	if err != nil {
		return nil, err
	}
	p, err := authz.Decode(body)
	if err != nil {
		return nil, fmt.Errorf("bootstrap: %w", err)
	}
	return &p, nil
}

// Leases opens the device lease area shared by every gatework tool, so a
// fabric change and a change by another tool exclude each other.
func Leases(environ map[string]string, home string) (*lease.Manager, error) {
	dir := nbootstrap.Env{App: "fabric", Environ: environ, Home: home}.SharedDir(nbootstrap.LeaseArea)
	if dir == "" {
		return nil, fmt.Errorf("bootstrap: no home directory for the shared lease area")
	}
	st, err := filelease.Open(dir)
	if err != nil {
		return nil, fmt.Errorf("bootstrap: leases: %w", err)
	}
	return lease.NewManager(st, nil), nil
}
