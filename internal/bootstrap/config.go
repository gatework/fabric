package bootstrap

import (
	"cmp"
	"context"
	"fmt"
	"path/filepath"

	nbootstrap "github.com/gatework/netops/bootstrap"
)

// App is the tool name in the shared gatework layout.
const App = "fabric"

// Config is fabric.yml: the files and directories commands use when their
// flag is not given. It is found as $FABRIC_CONFIG, ./fabric.yml, then
// ~/.gatework/fabric.yml; relative paths resolve against the file's
// directory and "~" against the home directory.
type Config struct {
	// Path is the configuration file read, or "" when none was found.
	Path string `yaml:"-"`

	Intent     string `yaml:"intent,omitempty"`      // --intent
	Baseline   string `yaml:"baseline,omitempty"`    // --baseline
	Policy     string `yaml:"policy,omitempty"`      // --policy
	Snapshot   string `yaml:"snapshot,omitempty"`    // --snapshot: the snapshot directory to read
	KnownHosts string `yaml:"known_hosts,omitempty"` // --known-hosts
	// BackupSnapshot is the snapshot directory used by fabric backup.
	BackupSnapshot string `yaml:"backup_snapshot,omitempty"`

	// Output roots; each defaults to a directory under
	// ~/.local/share/gatework/fabric.
	Snapshots string `yaml:"snapshots,omitempty"` // collect --output
	Plans     string `yaml:"plans,omitempty"`     // plan --output
	Journal   string `yaml:"journal,omitempty"`   // deploy --journal

	env map[string]string
}

// LoadConfig discovers and reads fabric.yml and the credentials file
// (.env beside it, ~/.gatework/.env by default). A discovered file that does
// not exist is not an error; an explicit $FABRIC_CONFIG that does not exist
// is.
func LoadConfig(ctx context.Context, environ map[string]string, home string) (Config, error) {
	e := nbootstrap.Env{App: App, Environ: environ, Home: home}
	var c Config
	path := e.Discover()
	explicit := environ["FABRIC_CONFIG"] != ""
	if path == "" {
		path = e.ConfigPath()
	}
	if path != "" {
		found, err := nbootstrap.ReadYAML(ctx, path, !explicit, &c)
		if err != nil {
			return Config{}, err
		}
		if found {
			c.Path = path
		}
	}
	base := "."
	if c.Path != "" {
		base = filepath.Dir(c.Path)
	}
	for _, p := range []*string{&c.Intent, &c.Baseline, &c.Policy, &c.Snapshot, &c.KnownHosts, &c.BackupSnapshot, &c.Snapshots, &c.Plans, &c.Journal} {
		*p = nbootstrap.ResolvePath(*p, base, home)
	}
	if data := e.DataDir(); data != "" {
		c.Snapshots = cmp.Or(c.Snapshots, filepath.Join(data, "snapshots"))
		c.Plans = cmp.Or(c.Plans, filepath.Join(data, "plans"))
		c.Journal = cmp.Or(c.Journal, filepath.Join(data, "executions"))
	}
	env, err := nbootstrap.LoadEnv(ctx, "", cmp.Or(c.Path, e.ConfigPath()), environ)
	if err != nil {
		return Config{}, fmt.Errorf("bootstrap: credentials: %w", err)
	}
	c.env = env
	return c, nil
}

// Lookup returns a variable of the process environment overlaid on the
// credentials file.
func (c Config) Lookup(name string) (string, bool) {
	v, ok := c.env[name]
	return v, ok
}

// Defaults maps flag names to their configured defaults, per command path
// ("fabric collect") for the output roots and for every command otherwise.
func (c Config) Defaults() (shared map[string]string, byCommand map[string]map[string]string) {
	shared = map[string]string{
		"intent": c.Intent, "baseline": c.Baseline, "policy": c.Policy, "snapshot": c.Snapshot, "known-hosts": c.KnownHosts,
	}
	byCommand = map[string]map[string]string{
		"fabric backup":  {"snapshot": c.BackupSnapshot},
		"fabric collect": {"output": c.Snapshots},
		"fabric plan":    {"output": c.Plans},
		"fabric deploy":  {"journal": c.Journal},
	}
	return shared, byCommand
}
