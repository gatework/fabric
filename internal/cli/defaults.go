package cli

import (
	"github.com/spf13/cobra"

	"github.com/gatework/fabric/internal/bootstrap"
)

// applyDefaults sets the configured files and directories as the defaults
// of flags the user did not set. A flag satisfied by the configuration is
// no longer required.
func applyDefaults(root *cobra.Command, cfg bootstrap.Config) {
	shared, byCommand := cfg.Defaults()
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		set := func(name, value string) {
			f := c.Flags().Lookup(name)
			if f == nil || f.Changed || value == "" || f.Value.Type() != "string" {
				return
			}
			if err := f.Value.Set(value); err != nil {
				return
			}
			f.DefValue = value
			delete(f.Annotations, cobra.BashCompOneRequiredFlag)
		}
		overrides := byCommand[c.CommandPath()]
		for name, value := range shared {
			if _, overridden := overrides[name]; !overridden {
				set(name, value)
			}
		}
		for name, value := range overrides {
			set(name, value)
		}
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(root)
}
