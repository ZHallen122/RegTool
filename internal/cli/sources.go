package cli

import (
	"github.com/ZHallen122/RegTool/source"

	"github.com/spf13/cobra"
)

func newSourcesCommand() *cobra.Command {
	var asJSON bool

	cmd := &cobra.Command{
		Use:   "sources",
		Short: "Show where the registry mirror list comes from",
		Long: "sources reports which list of mirrors regtool is working from: the URL\n" +
			"it fetches, where this run's list actually came from, the cache file\n" +
			"behind it and how many regions and apps it covers.\n\n" +
			"The list is looked for in this order: the file named by\n" +
			"REGTOOL_SOURCES_FILE, then the URL in REGTOOL_SOURCES_URL or the\n" +
			"built-in default, then the local cache, then the copy compiled into\n" +
			"the binary. REGTOOL_OFFLINE=1 skips the network and REGTOOL_CONFIG_DIR\n" +
			"moves the cache.\n\n" +
			"Run `regtool sources refresh` to fetch the list again even when the\n" +
			"cache looks current.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			status, err := source.NewLoader().Describe(cmd.Context())
			if err != nil {
				return err
			}
			if asJSON {
				return writeJSON(cmd.OutOrStdout(), status)
			}
			return writeSourcesStatus(cmd.OutOrStdout(), status)
		},
	}

	cmd.Flags().BoolVar(&asJSON, "json", false, "print the result as JSON")
	cmd.AddCommand(newSourcesRefreshCommand())
	return cmd
}

func newSourcesRefreshCommand() *cobra.Command {
	var asJSON bool

	cmd := &cobra.Command{
		Use:   "refresh",
		Short: "Fetch the mirror list again and rewrite the cache",
		Long: "refresh fetches the sources document without asking the server whether\n" +
			"the cached copy is still current, replaces the cache with what came\n" +
			"back and reports whether anything changed.\n\n" +
			"Unlike the other commands it does not fall back: a fetch that fails is\n" +
			"an error, because keeping the cache is what refresh was asked not to do.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			result, err := source.NewLoader().Refresh(cmd.Context())
			if err != nil {
				return err
			}
			if asJSON {
				return writeJSON(cmd.OutOrStdout(), result)
			}
			return writeSourcesRefresh(cmd.OutOrStdout(), result)
		},
	}

	cmd.Flags().BoolVar(&asJSON, "json", false, "print the result as JSON")
	return cmd
}
