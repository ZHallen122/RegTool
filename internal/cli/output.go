package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/ZHallen122/RegTool/internal/service"
)

// unknownValue stands in for a column the tool could not fill in.
const unknownValue = "-"

// writeJSON prints v as indented JSON. It is the only thing --json writes, and
// it always writes to stdout.
func writeJSON(w io.Writer, v any) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(v); err != nil {
		return fmt.Errorf("failed to encode JSON output: %w", err)
	}
	return nil
}

// newTable returns a tabwriter configured the same way for every table.
func newTable(w io.Writer) *tabwriter.Writer {
	return tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
}

func writeStatuses(w io.Writer, statuses []service.AppStatus) error {
	if len(statuses) == 0 {
		fmt.Fprintln(w, "no supported package manager was found on this machine")
		return nil
	}

	table := newTable(w)
	fmt.Fprintln(table, "APP\tREGION\tURL")
	for _, status := range statuses {
		url := status.URL
		if status.Err != nil {
			url = "error: " + status.Err.Error()
		} else if url == "" {
			url = unknownValue
		}
		fmt.Fprintf(table, "%s\t%s\t%s\n", status.App, status.Region, url)
	}
	return flush(table)
}

func writeEntries(w io.Writer, entries []service.RegistryEntry) error {
	if len(entries) == 0 {
		fmt.Fprintln(w, "no registry sources are known")
		return nil
	}

	table := newTable(w)
	fmt.Fprintln(table, "APP\tREGION\tURL")
	for _, entry := range entries {
		fmt.Fprintf(table, "%s\t%s\t%s\n", entry.App, entry.Region, entry.URL)
	}
	return flush(table)
}

func writeChanges(w io.Writer, results []service.ChangeResult, dryRun bool) error {
	if len(results) == 0 {
		fmt.Fprintln(w, "nothing to change")
		return nil
	}

	if dryRun {
		fmt.Fprintln(w, "dry run: no configuration was changed")
	}

	table := newTable(w)
	fmt.Fprintln(table, "APP\tFROM\tTO\tRESULT")
	for _, result := range results {
		fmt.Fprintf(table, "%s\t%s\t%s\t%s\n",
			result.App, orUnknown(result.From), orUnknown(result.To), changeOutcome(result, dryRun))
	}
	return flush(table)
}

func changeOutcome(result service.ChangeResult, dryRun bool) string {
	switch {
	case result.Err != nil:
		return "error: " + result.Err.Error()
	case dryRun:
		return "would change"
	default:
		return "changed"
	}
}

func orUnknown(value string) string {
	if value == "" {
		return unknownValue
	}
	return value
}

func flush(table *tabwriter.Writer) error {
	if err := table.Flush(); err != nil {
		return fmt.Errorf("failed to write output: %w", err)
	}
	return nil
}
