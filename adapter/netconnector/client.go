package netconnector

import (
	"context"
	"errors"
	"fmt"

	"github.com/gatework/net-connector/connector"
	"github.com/gatework/netops/apply"
	"github.com/gatework/netops/change"
	ndevice "github.com/gatework/netops/device"
	"github.com/gatework/netops/failure"

	"github.com/gatework/fabric/device"
	"github.com/gatework/fabric/vendors/nxos"
)

// Client opens net-connector sessions to one switch. Each method opens and
// closes its own session; paging, width, checkpoint and save dialogues
// belong to the net-connector NX-OS driver.
type Client struct {
	d   *connector.Dialer
	dev ndevice.Device
}

// New returns a client for the switch dev, which must have the NX-OS
// platform.
func New(d *connector.Dialer, dev ndevice.Device) *Client { return &Client{d: d, dev: dev} }

// Run executes read-only commands in one session and returns their text by
// command. A command the switch rejects is an error.
func (c *Client) Run(ctx context.Context, commands []string) (map[string]string, error) {
	var out map[string]string
	err := c.with(ctx, func(ctx context.Context, s *connector.Session) error {
		var err error
		out, err = collect(ctx, s, commands)
		return err
	})
	return out, err
}

// Collect reads every dimension the configuration does not prove absent,
// the configuration first, and returns the raw outputs keyed by command.
// Outputs read before a failure are kept.
func (c *Client) Collect(ctx context.Context) (map[string]string, error) {
	out := map[string]string{}
	err := c.with(ctx, func(ctx context.Context, s *connector.Session) error {
		var cfg *device.Config
		for _, p := range nxos.Probes() {
			if nxos.Absent(cfg, p.Dimension) {
				continue
			}
			got, err := collect(ctx, s, []string{p.Command})
			if err != nil {
				return err
			}
			out[p.Command] = got[p.Command]
			if p.Dimension == device.DimConfig {
				cfg, _ = nxos.ParseConfig(ctx, got[p.Command])
			}
		}
		return nil
	})
	return out, err
}

// Checkpoint reads the running configuration and records it under name,
// in one session.
func (c *Client) Checkpoint(ctx context.Context, name string) (*device.Config, error) {
	var cfg *device.Config
	err := c.with(ctx, func(ctx context.Context, s *connector.Session) error {
		var err error
		if cfg, err = readConfig(ctx, s); err != nil {
			return err
		}
		return s.Checkpoint(ctx, name)
	})
	return cfg, err
}

// Rollback restores the checkpoint atomically and reads the configuration
// after it.
func (c *Client) Rollback(ctx context.Context, name string) (*device.Config, error) {
	var cfg *device.Config
	err := c.with(ctx, func(ctx context.Context, s *connector.Session) error {
		if err := s.Rollback(ctx, name); err != nil {
			return err
		}
		var err error
		cfg, err = readConfig(ctx, s)
		return err
	})
	return cfg, err
}

// Save copies the running configuration to startup; the driver requires
// the switch's "Copy complete".
func (c *Client) Save(ctx context.Context) error {
	return c.with(ctx, func(ctx context.Context, s *connector.Session) error { return s.Save(ctx) })
}

// Apply lends one session to netops apply.Run. It returns the operation
// error and the close error separately, as apply.Settle needs.
func (c *Client) Apply(ctx context.Context, run func(context.Context, apply.Session[*device.Config]) error) (opErr, closeErr error) {
	return c.d.Open(ctx, c.dev, func(ctx context.Context, s *connector.Session) error {
		return run(ctx, session{s})
	})
}

// with opens one session, runs fn and joins the operation and close errors.
func (c *Client) with(ctx context.Context, fn func(context.Context, *connector.Session) error) error {
	opErr, closeErr := c.d.Open(ctx, c.dev, fn)
	return errors.Join(opErr, closeErr)
}

// session adapts a connector session to apply.Session.
type session struct{ s *connector.Session }

var _ apply.Session[*device.Config] = session{}

func (a session) Read(ctx context.Context) (*device.Config, error) { return readConfig(ctx, a.s) }

func (a session) Write(ctx context.Context, cmd change.Command) error { return a.s.Write(ctx, cmd) }

func collect(ctx context.Context, s *connector.Session, commands []string) (map[string]string, error) {
	c, err := s.Collect(ctx, commands)
	if err != nil {
		return nxos.Outputs(ctx, c), err
	}
	for _, o := range c.Outputs {
		if !o.OK() {
			return nxos.Outputs(ctx, c), failure.New(o.Failure, "netconnector: "+o.Command)
		}
	}
	return nxos.Outputs(ctx, c), nil
}

func readConfig(ctx context.Context, s *connector.Session) (*device.Config, error) {
	out, err := collect(ctx, s, []string{"show running-config"})
	if err != nil {
		return nil, err
	}
	cfg, err := nxos.ParseConfig(ctx, out["show running-config"])
	if err != nil {
		return nil, fmt.Errorf("netconnector: %w", err)
	}
	return cfg, nil
}
