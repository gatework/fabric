package netconnector

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	expect "github.com/gatework/go-expect"
	"github.com/gatework/go-netops/apply"
	"github.com/gatework/net-connector/connector"
	"github.com/gatework/net-connector/vendors/cisco"

	"github.com/gatework/fabric/device"
	"github.com/gatework/fabric/vendors/nxos"
)

// ErrCheckpoint reports a checkpoint or rollback the switch did not confirm.
var ErrCheckpoint = errors.New("netconnector: checkpoint not confirmed")

// slowCommand bounds checkpoint and rollback, which take tens of seconds.
const slowCommand = 5 * time.Minute

// Client opens sessions to one switch.
type Client struct {
	conn connector.ConnectionConfig
}

// New returns a client for the connection.
func New(conn connector.ConnectionConfig) *Client { return &Client{conn: conn} }

// Device identifies the switch in execution journals.
func (c *Client) Device() apply.Device {
	return apply.Device{Host: c.conn.Host, Platform: nxos.Platform}
}

// With connects, lends one session to run and closes it.
func (c *Client) With(ctx context.Context, run func(context.Context, *Session) error) error {
	opErr, cleanupErr := c.open(ctx, run)
	return errors.Join(opErr, cleanupErr)
}

// Opener adapts the client to go-netops apply.
func (c *Client) Opener() apply.Opener[*device.Config] {
	return func(ctx context.Context, run func(context.Context, apply.Session[*device.Config]) error) (error, error) {
		return c.open(ctx, func(ctx context.Context, s *Session) error { return run(ctx, s) })
	}
}

// Session is one connected operation on a switch.
type Session struct {
	op *connector.Operation
}

var _ apply.Session[*device.Config] = (*Session)(nil)

// Show runs a read-only command and returns its output without the echo
// and prompt.
func (s *Session) Show(ctx context.Context, command string) (string, error) {
	return s.exec(ctx, command, nil)
}

// Prepare disables paging and wrapping.
func (s *Session) Prepare(ctx context.Context) error {
	for _, c := range nxos.Preparation() {
		if _, err := s.Show(ctx, c); err != nil {
			return err
		}
	}
	return nil
}

// ReadConfiguration reads and parses the running configuration.
func (s *Session) ReadConfiguration(ctx context.Context) (*device.Config, error) {
	text, err := s.Show(ctx, "show running-config")
	if err != nil {
		return nil, err
	}
	cfg, err := nxos.ParseConfig(text)
	if err != nil {
		return nil, fmt.Errorf("netconnector: %w", err)
	}
	return cfg, nil
}

// Write sends one configuration line.
func (s *Session) Write(ctx context.Context, command string) error {
	res, err := s.op.ExecuteCommand(ctx, connector.Command{Text: command, OutputSensitive: true, Source: "fabric"}, connector.ExecuteOptions{SkipPrivilege: true})
	if err != nil {
		return fmt.Errorf("netconnector: %w", err)
	}
	if _, err := res.SingleAccepted(command); err != nil {
		return fmt.Errorf("netconnector: %w", err)
	}
	return nil
}

// Checkpoint records the running configuration under name.
func (s *Session) Checkpoint(ctx context.Context, name string) error {
	return s.confirm(ctx, nxos.CheckpointCommand(name))
}

// Rollback atomically restores the named checkpoint.
func (s *Session) Rollback(ctx context.Context, name string) error {
	return s.confirm(ctx, nxos.RollbackCommand(name))
}

// Save copies the running configuration to startup.
func (s *Session) Save(ctx context.Context) error {
	if _, err := s.op.SaveConfig(ctx, connector.ExecuteOptions{SkipPrivilege: true}); err != nil {
		return fmt.Errorf("netconnector: save: %w", err)
	}
	return nil
}

// Collect reads every dimension the configuration does not prove absent
// and returns the raw outputs keyed by command, the configuration first.
func (s *Session) Collect(ctx context.Context) (map[string]string, error) {
	if err := s.Prepare(ctx); err != nil {
		return nil, err
	}
	out := map[string]string{}
	var cfg *device.Config
	for _, p := range nxos.Probes() {
		if nxos.Absent(cfg, p.Dimension) {
			continue
		}
		text, err := s.Show(ctx, p.Command)
		if err != nil {
			return out, err
		}
		out[p.Command] = text
		if p.Dimension == device.DimConfig {
			cfg, _ = nxos.ParseConfig(text)
		}
	}
	return out, nil
}

// Run executes read-only commands in one session.
func (c *Client) Run(ctx context.Context, commands []string) (map[string]string, error) {
	out := map[string]string{}
	err := c.With(ctx, func(ctx context.Context, s *Session) error {
		if err := s.Prepare(ctx); err != nil {
			return err
		}
		for _, cmd := range commands {
			text, err := s.Show(ctx, cmd)
			if err != nil {
				return err
			}
			out[cmd] = text
		}
		return nil
	})
	return out, err
}

// Collect reads every dimension in one session.
func (c *Client) Collect(ctx context.Context) (map[string]string, error) {
	var out map[string]string
	err := c.With(ctx, func(ctx context.Context, s *Session) error {
		var err error
		out, err = s.Collect(ctx)
		return err
	})
	return out, err
}

// Checkpoint reads the running configuration and then records it under
// name, in one session.
func (c *Client) Checkpoint(ctx context.Context, name string) (*device.Config, error) {
	var cfg *device.Config
	err := c.With(ctx, func(ctx context.Context, s *Session) error {
		if err := s.Prepare(ctx); err != nil {
			return err
		}
		var err error
		if cfg, err = s.ReadConfiguration(ctx); err != nil {
			return err
		}
		return s.Checkpoint(ctx, name)
	})
	return cfg, err
}

// Rollback restores the checkpoint and reads the configuration after it.
func (c *Client) Rollback(ctx context.Context, name string) (*device.Config, error) {
	var cfg *device.Config
	err := c.With(ctx, func(ctx context.Context, s *Session) error {
		if err := s.Prepare(ctx); err != nil {
			return err
		}
		if err := s.Rollback(ctx, name); err != nil {
			return err
		}
		var err error
		cfg, err = s.ReadConfiguration(ctx)
		return err
	})
	return cfg, err
}

// Save copies the running configuration to startup in one session.
func (c *Client) Save(ctx context.Context) error {
	return c.With(ctx, func(ctx context.Context, s *Session) error { return s.Save(ctx) })
}

func (c *Client) open(ctx context.Context, run func(context.Context, *Session) error) (opErr, cleanupErr error) {
	dev, err := connector.New(cisco.NXOS(), c.conn)
	if err != nil {
		return fmt.Errorf("netconnector: %w", err), nil
	}
	defer func() { cleanupErr = dev.Close() }()
	if err := dev.Connect(ctx); err != nil {
		return fmt.Errorf("netconnector: connect: %w", err), nil
	}
	opErr = dev.WithOperation(ctx, func(ctx context.Context, op *connector.Operation) error {
		return run(ctx, &Session{op: op})
	})
	return opErr, nil
}

func (s *Session) exec(ctx context.Context, command string, timeout *time.Duration) (string, error) {
	cmd := connector.Command{Text: command, OutputSensitive: true, Timeout: timeout, Source: "fabric"}
	if prompt := s.op.RawPrompt(); prompt != "" {
		cmd.Prompt = expect.Line(prompt)
	}
	res, err := s.op.ExecuteCommand(ctx, cmd, connector.ExecuteOptions{SkipPrivilege: true})
	if err != nil {
		return "", fmt.Errorf("netconnector: %w", err)
	}
	step, err := res.SingleAccepted(command)
	if err != nil {
		return "", fmt.Errorf("netconnector: %w", err)
	}
	body, err := step.Body()
	if err != nil {
		return "", fmt.Errorf("netconnector: %w", err)
	}
	return strings.ReplaceAll(body, "\r", ""), nil
}

func (s *Session) confirm(ctx context.Context, command string) error {
	timeout := slowCommand
	out, err := s.exec(ctx, command, &timeout)
	if err != nil {
		return err
	}
	if !nxos.Confirmed(out) {
		return fmt.Errorf("%w: %s", ErrCheckpoint, command)
	}
	return nil
}
