package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/ZHallen122/RegTool/internal/history"
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

// writeProbeReports prints what every mirror answered, grouped by app and
// quickest first, the way Doctor already ordered them.
func writeProbeReports(w io.Writer, reports []service.ProbeReport) error {
	if len(reports) == 0 {
		fmt.Fprintln(w, "no registry sources are known")
		return nil
	}

	table := newTable(w)
	fmt.Fprintln(table, "APP\tREGION\tURL\tLATENCY\tSTATUS")
	for _, report := range reports {
		latency := formatLatency(report.Latency)
		status := report.Status
		if !report.OK() {
			// A failed probe's latency only says how long it took to give up,
			// which is worse than saying nothing.
			latency = unknownValue
			status = "error: " + probeReason(report)
		}
		fmt.Fprintf(table, "%s\t%s\t%s\t%s\t%s\n", report.App, report.Region, report.URL, latency, status)
	}
	return flush(table)
}

// probeReason is the short explanation shown in the STATUS column.
func probeReason(report service.ProbeReport) string {
	if report.Reason != "" {
		return report.Reason
	}
	if report.Err != nil {
		return report.Err.Error()
	}
	return "unreachable"
}

// formatLatency renders a measured round trip at a precision that means
// something: a network measurement's microseconds are noise, but a local mirror
// can answer in well under a millisecond.
func formatLatency(d time.Duration) string {
	switch {
	case d <= 0:
		return unknownValue
	case d < time.Millisecond:
		return d.Round(10 * time.Microsecond).String()
	default:
		return d.Round(time.Millisecond).String()
	}
}

// writeUseResult prints what a `use` run did, or would do: one row per app and,
// for a dry run, the unified diff of every file that would be rewritten.
func writeUseResult(w io.Writer, result *service.UseResult) error {
	if len(result.Changes) == 0 {
		fmt.Fprintln(w, "nothing to change")
		return nil
	}

	if result.DryRun {
		fmt.Fprintln(w, "dry run: no configuration was changed")
	}

	// A run that chose the regions itself owes the user a column saying which
	// ones it chose, and how quick they were.
	showRegion := result.Fastest

	table := newTable(w)
	if showRegion {
		fmt.Fprintln(table, "APP\tREGION\tFROM\tTO\tRESULT")
	} else {
		fmt.Fprintln(table, "APP\tFROM\tTO\tRESULT")
	}
	for _, change := range result.Changes {
		if showRegion {
			fmt.Fprintf(table, "%s\t%s\t%s\t%s\t%s\n",
				change.App, chosenRegion(change), orUnknown(change.From), orUnknown(change.To),
				changeOutcome(change, result.DryRun))
			continue
		}
		fmt.Fprintf(table, "%s\t%s\t%s\t%s\n",
			change.App, orUnknown(change.From), orUnknown(change.To), changeOutcome(change, result.DryRun))
	}
	if err := flush(table); err != nil {
		return err
	}

	if result.DryRun {
		for _, change := range result.Changes {
			if change.Diff == "" {
				continue
			}
			fmt.Fprintf(w, "\n%s:\n%s", change.App, change.Diff)
		}
		return nil
	}

	if result.SnapshotID != "" {
		fmt.Fprintf(w, "\nsnapshot %s: run 'regtool undo' to put it back\n", result.SnapshotID)
	}
	return nil
}

// writeSnapshots prints the snapshot history, newest first.
func writeSnapshots(w io.Writer, snapshots []history.Snapshot) error {
	if len(snapshots) == 0 {
		fmt.Fprintln(w, "regtool has not changed anything yet")
		return nil
	}

	table := newTable(w)
	fmt.Fprintln(table, "ID\tCREATED\tNOTE\tFILES")
	for _, snapshot := range snapshots {
		fmt.Fprintf(table, "%s\t%s\t%s\t%s\n",
			snapshot.ID,
			snapshot.CreatedAt.Local().Format(time.RFC3339),
			orUnknown(snapshot.Note),
			snapshotFiles(snapshot))
	}
	return flush(table)
}

// snapshotFiles names the files a snapshot captured, by base name so the table
// stays readable.
func snapshotFiles(snapshot history.Snapshot) string {
	if len(snapshot.Files) == 0 {
		return unknownValue
	}
	names := make([]string, 0, len(snapshot.Files))
	for _, file := range snapshot.Files {
		names = append(names, filepath.Base(file.Path))
	}
	return strings.Join(names, ", ")
}

// writeRestored reports what an undo put back.
func writeRestored(w io.Writer, snapshot *history.Snapshot) error {
	fmt.Fprintf(w, "restored snapshot %s (%s)\n", snapshot.ID, orUnknown(snapshot.Note))
	for _, file := range snapshot.Files {
		state := "restored"
		if !file.Existed {
			state = "removed"
		}
		fmt.Fprintf(w, "  %s %s\n", state, file.Path)
	}
	return nil
}

func changeOutcome(change service.ChangeResult, dryRun bool) string {
	switch {
	case change.Err != nil:
		return "error: " + change.Err.Error()
	case change.Noop:
		return "already set"
	case dryRun:
		return "would change"
	default:
		return "changed"
	}
}

// chosenRegion renders the region --fastest settled on, with the latency that
// won it the job.
func chosenRegion(change service.ChangeResult) string {
	if change.Region == "" {
		return unknownValue
	}
	if change.Latency <= 0 {
		return change.Region
	}
	return fmt.Sprintf("%s (%s)", change.Region, formatLatency(change.Latency))
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
