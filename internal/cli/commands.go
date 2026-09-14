package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newUseCommand() *cobra.Command {
	var (
		dryRun bool
		asJSON bool
	)

	cmd := &cobra.Command{
		Use:   "use <region> [app...]",
		Short: "Point package managers at a region's mirrors",
		Long: "use switches the named apps to the mirrors of a region.\n" +
			"With no app names every installed app is switched.\n\n" +
			"Every file that is about to change is snapshotted first, so the whole\n" +
			"run can be rolled back with `regtool undo`. With --dry-run nothing is\n" +
			"written and the diff of every file is printed instead.",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, err := loadService(cmd.Context())
			if err != nil {
				return err
			}

			// A validation failure comes back without a result and there is
			// nothing to print; anything else is reported per app, so the
			// table is printed before the error is returned.
			result, useErr := svc.Use(cmd.Context(), args[0], args[1:], dryRun)
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
	return cmd
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
			fmt.Fprintln(cmd.OutOrStdout(), Version)
			return nil
		},
	}
}
