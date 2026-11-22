package cli

import (
	"context"
	"strings"

	"github.com/spf13/cobra"

	"github.com/gatework/fabric"
	"github.com/gatework/fabric/internal/bootstrap"
)

// completionIntent reads the intent a command would use: --intent, or the
// fabric.yml default. Completion never fails loudly: without an intent it
// offers nothing.
func completionIntent(ctx context.Context, e env, cmd *cobra.Command) *fabric.Intent {
	path := ""
	if f := cmd.Flags().Lookup("intent"); f != nil && f.Changed {
		path = f.Value.String()
	}
	if path == "" {
		cfg, err := bootstrap.LoadConfig(ctx, e.environ, e.home)
		if err != nil {
			return nil
		}
		shared, byCommand := cfg.Defaults()
		path = shared["intent"]
		if p := byCommand[cmd.CommandPath()]["intent"]; p != "" {
			path = p
		}
	}
	if path == "" {
		return nil
	}
	in, err := bootstrap.ReadIntent(path)
	if err != nil {
		return nil
	}
	return in
}

// switchNames returns "name\trole region mgmt" completions.
func switchNames(ctx context.Context, e env, cmd *cobra.Command, prefix string) []cobra.Completion {
	in := completionIntent(ctx, e, cmd)
	if in == nil {
		return nil
	}
	var out []cobra.Completion
	for _, r := range in.Regions {
		for _, s := range r.Switches {
			if strings.HasPrefix(strings.ToLower(s.Name), strings.ToLower(prefix)) {
				out = append(out, cobra.CompletionWithDesc(s.Name, s.Role.String()+" "+r.Name+" "+s.Mgmt.String()))
			}
		}
	}
	return out
}

// completeSwitches completes a flag with the intent's switch names.
func completeSwitches(e env, c *cobra.Command, flag string) {
	_ = c.RegisterFlagCompletionFunc(flag, func(cmd *cobra.Command, _ []string, prefix string) ([]cobra.Completion, cobra.ShellCompDirective) {
		return switchNames(cmd.Context(), e, cmd, prefix), cobra.ShellCompDirectiveNoFileComp
	})
}

// completeRegions completes a flag with the intent's region names.
func completeRegions(e env, c *cobra.Command, flag string) {
	_ = c.RegisterFlagCompletionFunc(flag, func(cmd *cobra.Command, _ []string, _ string) ([]cobra.Completion, cobra.ShellCompDirective) {
		in := completionIntent(cmd.Context(), e, cmd)
		if in == nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		var out []cobra.Completion
		for _, r := range in.Regions {
			out = append(out, cobra.CompletionWithDesc(r.Name, "区域"))
		}
		return out, cobra.ShellCompDirectiveNoFileComp
	})
}

// switchArg completes a positional switch name.
func switchArg(e env) cobra.CompletionFunc {
	return func(cmd *cobra.Command, args []string, prefix string) ([]cobra.Completion, cobra.ShellCompDirective) {
		if len(args) > 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		return switchNames(cmd.Context(), e, cmd, prefix), cobra.ShellCompDirectiveNoFileComp
	}
}
