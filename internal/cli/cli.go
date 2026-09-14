// Package cli is the cobra front end. It parses arguments, calls the service
// layer and renders the result; with no arguments at all it hands over to the
// interactive TUI.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/ZHallen122/RegTool/internal/service"
	"github.com/ZHallen122/RegTool/internal/tui"

	"github.com/spf13/cobra"
)

// Version is the version reported by `regtool version`. Release builds
// overwrite it with -ldflags "-X github.com/ZHallen122/RegTool/internal/cli.Version=...".
var Version = "dev"

// Main runs regtool with the given arguments and returns the process exit
// code. Every stream is injected so end-to-end tests can drive it.
func Main(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	root := newRootCommand(stdin, stdout, stderr)
	root.SetArgs(args)

	if err := root.ExecuteContext(context.Background()); err != nil {
		fmt.Fprintln(stderr, "regtool:", err)
		return 1
	}
	return 0
}

func newRootCommand(stdin io.Reader, stdout, stderr io.Writer) *cobra.Command {
	root := &cobra.Command{
		Use:   "regtool",
		Short: "Switch package manager registries between regional mirrors",
		Long: "regtool points npm, yarn, pip, gem and homebrew at the registry mirror\n" +
			"closest to you. Run it without arguments for the interactive interface.",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(_ *cobra.Command, _ []string) error {
			return tui.Run()
		},
	}

	root.SetIn(stdin)
	root.SetOut(stdout)
	root.SetErr(stderr)

	root.AddCommand(
		newUseCommand(),
		newStatusCommand(),
		newListCommand(),
		newHistoryCommand(),
		newUndoCommand(),
		newRefreshCommand(),
		newVersionCommand(),
	)
	return root
}

// loadService builds the service the subcommands run against.
func loadService(ctx context.Context) (*service.Service, error) {
	return service.Load(ctx)
}

// statusErrors does the same for the per-app failures of a status run.
func statusErrors(statuses []service.AppStatus) error {
	var errs []error
	for _, status := range statuses {
		if status.Err != nil {
			errs = append(errs, status.Err)
		}
	}
	return errors.Join(errs...)
}
