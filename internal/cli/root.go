package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	nbootstrap "github.com/gatework/netops/bootstrap"

	"github.com/gatework/fabric/internal/bootstrap"
)

// Exit codes.
const (
	ExitOK         = 0
	ExitIncomplete = 1
	ExitError      = 2
	ExitFindings   = 3
	ExitSignal     = 130
)

// exitError carries a specific exit code with an optional message.
type exitError struct {
	code int
	msg  string
}

func (e *exitError) Error() string { return e.msg }

func exit(code int, format string, args ...any) error {
	return &exitError{code: code, msg: fmt.Sprintf(format, args...)}
}

// env abstracts process state for tests.
type env struct {
	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer
	lookup func(string) (string, bool)
	tty    bool
	// environ and home locate fabric.yml and the data directory; empty in
	// tests, where flags carry everything.
	environ map[string]string
	home    string
}

// Run executes the command line and returns the process exit code.
func Run(ctx context.Context, args []string) int {
	info, err := os.Stdin.Stat()
	tty := err == nil && info.Mode()&os.ModeCharDevice != 0
	home, _ := os.UserHomeDir()
	return run(ctx, args, env{stdin: os.Stdin, stdout: os.Stdout, stderr: os.Stderr, lookup: os.LookupEnv, tty: tty,
		environ: nbootstrap.Environ(os.Environ()), home: home})
}

func run(ctx context.Context, args []string, e env) int {
	lookup := e.lookup
	e.lookup = func(name string) (string, bool) {
		return lookup(name)
	}
	root := newRoot(e) //nolint:contextcheck // Cobra passes ctx to every command through cmd.Context().
	// Cobra handles --help before pre-run hooks and validates required flags after them.
	root.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
		if cmd.Parent() == root && cmd.Name() == "help" {
			return nil
		}
		cfg, err := bootstrap.LoadConfig(ctx, e.environ, e.home)
		if err != nil {
			return err
		}
		if cfg.Path != "" || e.home != "" {
			lookup = cfg.Lookup
		}
		applyDefaults(root, cfg)
		return nil
	}
	root.SetArgs(args)
	root.SetOut(e.stdout)
	root.SetErr(e.stderr)
	err := root.ExecuteContext(ctx)
	if err == nil {
		return ExitOK
	}
	if ctx.Err() != nil {
		_, _ = fmt.Fprintln(e.stderr, "已中断")
		return ExitSignal
	}
	if xe, ok := errors.AsType[*exitError](err); ok {
		if xe.msg != "" {
			_, _ = fmt.Fprintln(e.stderr, xe.msg)
		}
		return xe.code
	}
	_, _ = fmt.Fprintln(e.stderr, "错误："+err.Error())
	return ExitError
}

func newRoot(e env) *cobra.Command {
	root := &cobra.Command{
		Use:           "fabric",
		Short:         "多区域 Cisco Nexus VXLAN EVPN Fabric 控制器",
		Long:          "多区域 Cisco Nexus VXLAN EVPN Fabric 控制器：采集并检查 fabric，按意图与基线规划配置，分批次安全下发。",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(
		newParse(e), newCollect(e), newCheck(e), newBackup(e), newBaseline(e),
		newImport(e), newPlan(e), newDeploy(e), newRollback(e),
		newNetwork(e), newTenant(e), newSwitch(e), newPort(e),
		newStatus(e), newLocate(e), newTopology(e), newCapacity(e), newReport(e),
	)
	return root
}
