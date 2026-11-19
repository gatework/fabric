package bootstrap

import (
	"errors"
	"fmt"
	"net/netip"
	"os"
	"time"

	"github.com/gatework/go-netops/assess"
	"github.com/gatework/net-connector/connector"

	"github.com/gatework/fabric"
	"github.com/gatework/fabric/adapter/netconnector"
	"github.com/gatework/fabric/baseline"
	"github.com/gatework/fabric/internal/workflow"
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

// Dialer returns the deployment dialer.
func (s SSH) Dialer(c Credentials) workflow.Dialer {
	return func(addr netip.Addr) workflow.Switch { return netconnector.New(s.connection(addr, c)) }
}

// Collector returns the collection dialer.
func (s SSH) Collector(c Credentials) func(netip.Addr) workflow.Collector {
	return func(addr netip.Addr) workflow.Collector { return netconnector.New(s.connection(addr, c)) }
}

func (s SSH) connection(addr netip.Addr, c Credentials) connector.ConnectionConfig {
	return connector.ConnectionConfig{
		Host:           addr.String(),
		Username:       c.Username,
		Password:       c.Password,
		EnablePassword: c.Enable,
		Port:           s.Port,
		KnownHosts:     s.KnownHosts,
		HostKeyPolicy:  connector.HostKeyPolicy(s.HostKeyPolicy),
		LoginTimeout:   s.LoginTimeout,
		CommandTimeout: s.CommandTimeout,
	}
}
