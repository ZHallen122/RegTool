package cli

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ZHallen122/RegTool/internal/probe"
	"github.com/ZHallen122/RegTool/internal/service"

	"github.com/spf13/cobra"
)

func newDoctorCommand() *cobra.Command {
	var (
		asJSON      bool
		timeout     time.Duration
		concurrency int
	)

	cmd := &cobra.Command{
		Use:   "doctor [app...]",
		Short: "Measure how fast every known mirror answers",
		Long: "doctor probes every region's mirror of every app and reports how long each\n" +
			"one took to answer. With no app names every app regtool knows about is\n" +
			"probed, installed or not.\n\n" +
			"All the mirrors are probed at once, --concurrency of them at a time, and\n" +
			"each probe gives up after --timeout. A mirror that answers with anything\n" +
			"below HTTP 500 counts as reachable: several registries answer a bare\n" +
			"request with 403 or 404 and are perfectly usable.\n\n" +
			"The exit status is non-zero only when not a single mirror answered.",
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if concurrency < 1 {
				return fmt.Errorf("--concurrency must be at least 1, got %d", concurrency)
			}
			if timeout <= 0 {
				return fmt.Errorf("--timeout must be positive, got %s", timeout)
			}

			svc, err := loadProbingService(cmd.Context(), timeout, concurrency)
			if err != nil {
				return err
			}

			reports, err := svc.Doctor(cmd.Context(), args)
			if err != nil {
				return err
			}

			if asJSON {
				if err := writeJSON(cmd.OutOrStdout(), reports); err != nil {
					return err
				}
			} else if err := writeProbeReports(cmd.OutOrStdout(), reports); err != nil {
				return err
			}
			return doctorError(reports)
		},
	}

	cmd.Flags().BoolVar(&asJSON, "json", false, "print the result as JSON")
	cmd.Flags().DurationVar(&timeout, "timeout", probe.DefaultTimeout, "how long a single mirror may take to answer")
	cmd.Flags().IntVar(&concurrency, "concurrency", probe.DefaultConcurrency, "how many mirrors to probe at once")
	return cmd
}

// doctorError fails the command only when nothing at all answered. A single
// dead mirror is information, not a failure: reporting it is the whole point of
// the command.
func doctorError(reports []service.ProbeReport) error {
	if len(reports) == 0 {
		return errors.New("no mirrors are known, so there was nothing to probe")
	}
	for _, report := range reports {
		if report.OK() {
			return nil
		}
	}
	return fmt.Errorf("none of the %d known mirrors answered", len(reports))
}

// loadProbingService builds the service for the commands that probe. A zero
// timeout or concurrency leaves the probe package's own default in place.
func loadProbingService(ctx context.Context, timeout time.Duration, concurrency int) (*service.Service, error) {
	return service.Load(ctx, service.WithProbeOptions(probe.Options{
		Timeout:     timeout,
		Concurrency: concurrency,
		// Mirrors log their clients, and a version tells them which regtool
		// build the traffic came from.
		UserAgent: "regtool/" + Version,
	}))
}
