package cli

import (
	"fmt"
	"strings"

	"github.com/ZHallen122/RegTool/internal/service"

	"github.com/spf13/cobra"
)

func newUseCommand() *cobra.Command {
	var (
		dryRun  bool
		asJSON  bool
		fastest bool
	)

	cmd := &cobra.Command{
		Use:   "use <region|--fastest> [app...]",
		Short: "Point package managers at a region's mirrors",
		Long: "use switches the named apps to the mirrors of a region.\n" +
			"With no app names every installed app is switched.\n\n" +
			"With --fastest the region is left out and regtool picks it: it probes\n" +
			"every region's mirror of every selected app at once and points each app\n" +
			"at whichever of its own mirrors answered quickest, so different apps can\n" +
			"end up in different regions.\n\n" +
			"Every file that is about to change is snapshotted first, so the whole\n" +
			"run can be rolled back with `regtool undo`. With --dry-run nothing is\n" +
			"written and the diff of every file is printed instead.",
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			region, apps, err := useTargets(args, fastest)
			if err != nil {
				return err
			}

			svc, err := loadProbingService(cmd.Context(), 0, 0)
			if err != nil {
				return err
			}

			// A validation failure comes back without a result and there is
			// nothing to print; anything else is reported per app, so the
			// table is printed before the error is returned.
			var (
				result *service.UseResult
				useErr error
			)
			if fastest {
				result, useErr = svc.UseFastest(cmd.Context(), apps, dryRun)
			} else {
				result, useErr = svc.Use(cmd.Context(), region, apps, dryRun)
			}
			if result == nil {
				return useErr
			}

			if asJSON {
				if err := writeJSON(cmd.OutOrStdout(), result); err != nil {
					return err
				}
			} else if err := writeUseResult(cmd.OutOrStdout(), result); err != nil {
				return err
			}
			return useErr
		},
	}

	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show the diff of every change without writing anything")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the result as JSON")
	cmd.Flags().BoolVar(&fastest, "fastest", false, "probe every region and pick the quickest mirror for each app")
	return cmd
}

// useTargets splits the positional arguments of `use` into the region and the
// apps. The region is the first argument unless --fastest was passed, in which
// case there is no region to give and every argument is an app.
func useTargets(args []string, fastest bool) (region string, apps []string, err error) {
	if !fastest {
		if len(args) == 0 {
			return "", nil, fmt.Errorf("use needs a region (%s), or --fastest to measure them and pick one",
				strings.Join(service.Regions(), ", "))
		}
		return args[0], args[1:], nil
	}

	for _, arg := range args {
		if service.IsRegion(arg) {
			return "", nil, fmt.Errorf("--fastest chooses the region itself: drop %q or drop --fastest", arg)
		}
	}
	return "", args, nil
}

func newStatusCommand() *cobra.Command {
	var asJSON bool

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show the registry every installed app currently uses",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			svc, err := loadService(cmd.Context())
			if err != nil {
				return err
			}

			statuses, err := svc.Status(cmd.Context())
			if err != nil {
				return err
			}

			if asJSON {
				if err := writeJSON(cmd.OutOrStdout(), statuses); err != nil {
					return err
				}
			} else if err := writeStatuses(cmd.OutOrStdout(), statuses); err != nil {
				return err
			}
			return statusErrors(statuses)
		},
	}

	cmd.Flags().BoolVar(&asJSON, "json", false, "print the result as JSON")
	return cmd
}

func newListCommand() *cobra.Command {
	var asJSON bool

	cmd := &cobra.Command{
		Use:   "list [app]",
		Short: "List the known registry mirrors",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, err := loadService(cmd.Context())
			if err != nil {
				return err
			}

			app := ""
			if len(args) == 1 {
				app = args[0]
			}

			entries, err := svc.List(cmd.Context(), app)
			if err != nil {
				return err
			}

			if asJSON {
				return writeJSON(cmd.OutOrStdout(), entries)
			}
			return writeEntries(cmd.OutOrStdout(), entries)
		},
	}

	cmd.Flags().BoolVar(&asJSON, "json", false, "print the result as JSON")
	return cmd
}

func newHistoryCommand() *cobra.Command {
	var asJSON bool

	cmd := &cobra.Command{
		Use:   "history",
		Short: "List the snapshots regtool took before changing anything",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			svc, err := loadService(cmd.Context())
			if err != nil {
				return err
			}

			snapshots, err := svc.History(cmd.Context())
			if err != nil {
				return err
			}

			if asJSON {
				return writeJSON(cmd.OutOrStdout(), snapshots)
			}
			return writeSnapshots(cmd.OutOrStdout(), snapshots)
		},
	}

	cmd.Flags().BoolVar(&asJSON, "json", false, "print the result as JSON")
	return cmd
}

func newUndoCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "undo [snapshot-id]",
		Short: "Put the configuration back the way a snapshot found it",
		Long: "undo restores every file captured in a snapshot. With no argument it\n" +
			"restores the newest one. The restore is itself snapshotted first, so an\n" +
			"undo can be undone. Run `regtool history` to see the snapshot ids.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, err := loadService(cmd.Context())
			if err != nil {
				return err
			}

			id := ""
			if len(args) == 1 {
				id = args[0]
			}

			snapshot, err := svc.Undo(cmd.Context(), id)
			if err != nil {
				return err
			}
			return writeRestored(cmd.OutOrStdout(), snapshot)
		},
	}
}

func newRefreshCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "refresh",
		Short: "Record the registry every installed app currently uses",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			svc, err := loadService(cmd.Context())
			if err != nil {
				return err
			}
			if err := svc.Refresh(cmd.Context()); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "recorded the current registries")
			return nil
		},
	}
}

func newVersionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the regtool version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			fmt.Fprintln(cmd.OutOrStdout(), versionString())
			return nil
		},
	}
}
