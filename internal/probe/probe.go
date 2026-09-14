// Package probe measures how quickly a set of registry mirrors answers.
//
// It exists so that regtool can answer two questions without asking the user
// to guess: which mirrors are reachable from here (`regtool doctor`), and which
// one of them is quickest (`regtool use --fastest`).
//
// # Concurrency
//
// [Run] probes every target concurrently, with an [errgroup.Group] capping how
// many requests are in flight at once. The results slice is allocated up front
// and every worker writes to its own index, so no lock is needed and the
// results come back in the same order as the targets whatever order they
// finished in.
//
// # Failure is data, not an error
//
// A mirror that times out or refuses the connection is the interesting case,
// not an exceptional one, so Run never returns an error and never gives up
// early: every target gets a [Result], and a failed probe carries its reason in
// [Result.Err]. Cancelling the context short-circuits the targets that have not
// started yet, which then report the context's own error.
//
// # What counts as reachable
//
// Package registries answer a bare GET on their root very differently: npm
// returns a JSON index, the Go module proxy a 404, some CDNs a 403. None of
// that says the mirror is broken, and all of it proves a round trip completed,
// so any status below 500 counts as reachable. Only a transport failure or a
// 5xx marks a mirror unusable.
package probe

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"
)

// Defaults applied by [Options] to any field left at its zero value.
const (
	// DefaultConcurrency is how many probes are in flight at once.
	DefaultConcurrency = 8
	// DefaultTimeout is how long a single probe may take.
	DefaultTimeout = 5 * time.Second
	// DefaultUserAgent identifies regtool to the mirrors it probes.
	DefaultUserAgent = "regtool"
)

// maxBodyBytes is how much of the response body is read before it is thrown
// away. Something has to be read so the connection can be reused, but a
// registry index can be megabytes and none of it is wanted.
const maxBodyBytes = 4 << 10

// Target is one mirror to probe: which app it serves, which region it belongs
// to and the URL configured for it.
type Target struct {
	App    string `json:"app"`
	Region string `json:"region"`
	URL    string `json:"url"`
}

// Result is the outcome of probing one [Target].
type Result struct {
	Target

	// Latency is how long the mirror took to send its response headers. It is
	// the time until the request returned, so for a failed probe it is the time
	// until it failed.
	Latency time.Duration
	// StatusCode is the HTTP status the mirror answered with, 0 when the
	// request never got that far.
	StatusCode int
	// Err is the transport failure that stopped the probe, nil when the mirror
	// answered at all. A 5xx answer is not an error here; see [Result.OK].
	Err error
}

// OK reports whether the mirror is usable: it answered, and it did not answer
// with a server error.
func (r Result) OK() bool { return r.Err == nil && r.StatusCode < 500 }

// Reason is a short description of why the mirror is not usable, "" when it
// is. It is what a table cell shows: the underlying errors are long and full of
// addresses the user did not type.
func (r Result) Reason() string {
	if r.Err == nil {
		if r.StatusCode >= 500 {
			return fmt.Sprintf("http %d", r.StatusCode)
		}
		return ""
	}

	switch {
	case errors.Is(r.Err, context.DeadlineExceeded):
		return "timed out"
	case errors.Is(r.Err, context.Canceled):
		return "cancelled"
	case isConnRefused(r.Err):
		return "connection refused"
	}

	var dnsErr *net.DNSError
	if errors.As(r.Err, &dnsErr) {
		return "dns lookup failed"
	}

	// A net.Error that reports a timeout but does not wrap
	// context.DeadlineExceeded is what http.Client.Timeout produces.
	var netErr net.Error
	if errors.As(r.Err, &netErr) && netErr.Timeout() {
		return "timed out"
	}

	// Everything else is unwrapped once, to drop the "Get \"<url>\":" prefix
	// net/http puts on every transport failure.
	var urlErr *url.Error
	if errors.As(r.Err, &urlErr) {
		return urlErr.Err.Error()
	}
	return r.Err.Error()
}

// Options tunes a [Run]. Every field may be left at its zero value, in which
// case the corresponding default is used.
type Options struct {
	// Client performs the requests. It must be safe for concurrent use, which
	// every [http.Client] is. Defaults to a client with Timeout set.
	Client *http.Client
	// Concurrency caps how many probes are in flight at once. Defaults to
	// [DefaultConcurrency].
	Concurrency int
	// Timeout caps a single probe. The overall run is bounded by the context
	// the caller passes instead. Defaults to [DefaultTimeout].
	Timeout time.Duration
	// UserAgent is sent with every request. Defaults to [DefaultUserAgent].
	UserAgent string
}

// withDefaults fills in every field the caller left blank.
func (o Options) withDefaults() Options {
	if o.Timeout <= 0 {
		o.Timeout = DefaultTimeout
	}
	if o.Concurrency <= 0 {
		o.Concurrency = DefaultConcurrency
	}
	if o.UserAgent == "" {
		o.UserAgent = DefaultUserAgent
	}
	if o.Client == nil {
		// Redirects are followed, which is what http.Client does by default:
		// several mirrors redirect their root to a landing page and the
		// redirect is part of what a package manager would pay for.
		o.Client = &http.Client{Timeout: o.Timeout}
	}
	return o
}

// Run probes every target and returns one result per target, in the same order.
//
// It never returns early: a target that fails does not stop the others, and
// there is no error return because a failed probe is the answer, not a fault.
// Cancelling ctx makes the targets that have not started yet return the
// context's error immediately.
func Run(ctx context.Context, targets []Target, opts Options) []Result {
	opts = opts.withDefaults()

	results := make([]Result, len(targets))
	if len(targets) == 0 {
		return results
	}

	// The group's limit is the only thing bounding concurrency; Go blocks once
	// that many probes are running, which is fine because Run waits for all of
	// them anyway. WithContext is deliberately not used: it cancels the whole
	// group on the first error, and here one dead mirror must not cut the
	// others short.
	var group errgroup.Group
	group.SetLimit(opts.Concurrency)

	for i, target := range targets {
		group.Go(func() error {
			results[i] = probeOne(ctx, target, opts)
			return nil
		})
	}
	// Every goroutine returns nil, so the error is always nil.
	_ = group.Wait()

	return results
}

// Fastest returns the usable result with the lowest latency. The second return
// value is false when no target was reachable at all.
func Fastest(results []Result) (Result, bool) {
	var (
		best  Result
		found bool
	)
	for _, result := range results {
		if !result.OK() {
			continue
		}
		if !found || result.Latency < best.Latency {
			best, found = result, true
		}
	}
	return best, found
}

// probeOne performs a single probe. It returns a Result rather than an error
// because the caller records the failure instead of propagating it.
func probeOne(ctx context.Context, target Target, opts Options) Result {
	result := Result{Target: target}

	// A cancelled run skips the work rather than issuing a request that is
	// certain to fail.
	if err := ctx.Err(); err != nil {
		result.Err = err
		return result
	}

	address := ProbeURL(target.URL)
	if address == "" {
		result.Err = fmt.Errorf("there is no %s mirror URL to probe for the %s region", target.App, target.Region)
		return result
	}

	// Each probe gets its own deadline so one slow mirror cannot eat the
	// budget of the ones queued behind it.
	reqCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, address, nil)
	if err != nil {
		result.Err = fmt.Errorf("failed to build a probe request for %s: %w", address, err)
		return result
	}
	req.Header.Set("User-Agent", opts.UserAgent)

	// http.Client.Do returns as soon as the response headers have arrived, so
	// this measures the round trip without the body download.
	start := time.Now()
	resp, err := opts.Client.Do(req)
	result.Latency = time.Since(start)
	if err != nil {
		result.Err = fmt.Errorf("failed to reach %s: %w", address, err)
		return result
	}
	defer resp.Body.Close()

	result.StatusCode = resp.StatusCode
	// Draining a little of the body lets the connection go back to the pool;
	// the content itself is of no interest, and a registry index is large.
	_, _ = io.CopyN(io.Discard, resp.Body, maxBodyBytes)
	return result
}

// ProbeURL turns a configured registry URL into the URL to request.
//
// Cargo stores its index as "sparse+https://…", which is a cargo protocol
// marker rather than part of the address, and a URL that arrived without a
// scheme is assumed to be https. Everything else is already requestable as it
// stands: a Go proxy, an npm registry and a pip simple index all answer a plain
// GET on the URL the package manager is configured with.
func ProbeURL(raw string) string {
	address := strings.TrimPrefix(strings.TrimSpace(raw), "sparse+")
	if address == "" {
		return ""
	}
	if !strings.Contains(address, "://") {
		address = "https://" + address
	}
	return address
}
