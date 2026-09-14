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
			"With no app names every installed app is switched.",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, err := loadService(cmd.Context())
			if err != nil {
				return err
			}

			results, err := svc.Use(cmd.Context(), args[0], args[1:], dryRun)
			if err != nil {
				return err
			}

			if asJSON {
				if err := writeJSON(cmd.OutOrStdout(), results); err != nil {
					return err
				}
			} else if err := writeChanges(cmd.OutOrStdout(), results, dryRun); err != nil {
				return err
			}
			return changeErrors(results)
		},
	}

	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show what would change without touching any config")
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
