// Package hub is the regtool-hub service: it serves the mirror list the CLI
// reads, checks every mirror in it on a schedule, and exports what it finds as
// JSON and as Prometheus metrics.
//
// The three pieces are deliberately separable. [Store] is the SQLite history of
// checks, [Checker] is the periodic job that fills it, and [Server] is the HTTP
// surface over both. Each takes its collaborators as fields, so a test can
// drive a checker against an httptest mirror and read the result out of a
// temporary database without starting a server, or exercise the handlers
// against a store nothing is writing to.
package hub

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// sourcesMaxAge is how long a client may reuse /v1/sources without asking
// again. Mirror lists change on the order of weeks, and a client that does ask
// gets a cheap 304 thanks to the ETag.
const sourcesMaxAge = 300 * time.Second

// Server is the hub's HTTP surface.
type Server struct {
	http    *http.Server
	handler http.Handler
	log     *slog.Logger
}

// ServerConfig is everything [NewServer] needs. Sources, Store and Metrics are
// required.
type ServerConfig struct {
	// Addr is the listen address, in [net.Listen] form.
	Addr string
	// Sources is the sources.json body served verbatim by /v1/sources. Serving
	// the bytes rather than a re-encoded struct is what makes the ETag stable
	// and the response byte-identical to the file the CLI would otherwise
	// fetch from GitHub.
	Sources []byte
	// Store answers the health endpoints.
	Store *Store
	// Metrics backs /metrics and records every request.
	Metrics *Metrics
	// Ready reports whether the first check has finished. /readyz is 503 until
	// it returns true. A nil Ready means always ready.
	Ready func() bool
	// Logger receives one line per request. Defaults to the discarding logger.
	Logger *slog.Logger
}

// NewServer wires the routes and the middleware.
func NewServer(cfg ServerConfig) *Server {
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	if cfg.Ready == nil {
		cfg.Ready = func() bool { return true }
	}

	api := &api{
		sources:     cfg.Sources,
		sourcesETag: etag(cfg.Sources),
		store:       cfg.Store,
		metrics:     cfg.Metrics,
		ready:       cfg.Ready,
		log:         cfg.Logger,
	}

	// Method patterns (Go 1.22+) mean a POST to /v1/sources is a 405 from the
	// mux rather than a handler that has to check the method itself.
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/sources", api.getSources)
	mux.HandleFunc("GET /v1/health", api.getHealth)
	mux.HandleFunc("GET /v1/health/history", api.getHistory)
	mux.HandleFunc("GET /healthz", api.getLive)
	mux.HandleFunc("GET /readyz", api.getReady)
	mux.Handle("GET /metrics", promhttp.HandlerFor(cfg.Metrics.Registry(), promhttp.HandlerOpts{
		ErrorHandling: promhttp.ContinueOnError,
	}))
	// "/" matches everything the routes above did not, which is the only way
	// to give an unknown path the same JSON error shape as everything else.
	mux.HandleFunc("/", api.notFound)

	// Order matters. The request id is minted first so every layer inside can
	// log it; recovery sits inside the observability layer so a panicked
	// request is still counted as the 500 it turned into.
	handler := withRequestID(withObservability(withRecovery(mux, cfg.Logger), cfg.Metrics, cfg.Logger))

	return &Server{
		handler: handler,
		log:     cfg.Logger,
		http: &http.Server{
			Addr:    cfg.Addr,
			Handler: handler,
			// A public-facing server needs every one of these: without
			// ReadHeaderTimeout a single slow client holds a connection open
			// forever, which is the Slowloris shape.
			ReadHeaderTimeout: 5 * time.Second,
			ReadTimeout:       15 * time.Second,
			WriteTimeout:      30 * time.Second,
			IdleTimeout:       60 * time.Second,
			MaxHeaderBytes:    1 << 16,
		},
	}
}

// Handler is the fully wired handler, for tests that want an
// [net/http/httptest.Server] instead of a real listener.
func (s *Server) Handler() http.Handler { return s.handler }

// Serve serves on an already-open listener until ctx is cancelled, then drains
// for up to drain before giving up on the connections still in flight.
//
// It takes a listener rather than an address so a caller — a test, usually —
// can bind port 0 and find out which port it got.
func (s *Server) Serve(ctx context.Context, listener net.Listener, drain time.Duration) error {
	errs := make(chan error, 1)
	go func() {
		err := s.http.Serve(listener)
		// A Shutdown in progress is the expected way for Serve to end.
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		errs <- err
	}()

	select {
	case err := <-errs:
		if err != nil {
			return fmt.Errorf("failed to serve HTTP on %s: %w", listener.Addr(), err)
		}
		return nil
	case <-ctx.Done():
	}

	s.log.Info("shutting down", "drain", drain.String())
	// Shutdown's context bounds the drain, not the shutdown itself, so it must
	// not be the cancelled ctx that got us here.
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), drain)
	defer cancel()

	if err := s.http.Shutdown(shutdownCtx); err != nil {
		// The listener is closed either way; a deadline here means some
		// connection outlived the drain, which is worth reporting but is not a
		// startup-style failure.
		return fmt.Errorf("failed to drain the HTTP server within %s: %w", drain, err)
	}
	// Serve has returned by now; collecting it keeps the goroutine from
	// outliving the call.
	<-errs
	return nil
}

// ListenAndServe binds Addr and hands off to [Server.Serve].
func (s *Server) ListenAndServe(ctx context.Context, drain time.Duration) error {
	listener, err := net.Listen("tcp", s.http.Addr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", s.http.Addr, err)
	}
	s.log.Info("listening", "addr", listener.Addr().String())
	return s.Serve(ctx, listener, drain)
}

// api holds the handler dependencies.
type api struct {
	sources     []byte
	sourcesETag string
	store       *Store
	metrics     *Metrics
	ready       func() bool
	log         *slog.Logger
}

// getSources serves the mirror list with a strong ETag, so the CLI polling it
// every start pays for one conditional request rather than the whole body.
func (a *api) getSources(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("ETag", a.sourcesETag)
	w.Header().Set("Cache-Control", "public, max-age="+strconv.Itoa(int(sourcesMaxAge.Seconds())))
	w.Header().Set("Content-Type", "application/json")

	if matchesETag(r.Header.Get("If-None-Match"), a.sourcesETag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	w.Header().Set("Content-Length", strconv.Itoa(len(a.sources)))
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(a.sources); err != nil {
		a.log.Warn("failed to write the sources response", "error", err.Error())
	}
}

// healthResponse is the body of /v1/health and /v1/health/history.
type healthResponse struct {
	Count   int      `json:"count"`
	Results []Result `json:"results"`
}

// getHealth serves the newest result for every mirror.
func (a *api) getHealth(w http.ResponseWriter, r *http.Request) {
	results, err := a.store.Latest(r.Context())
	if err != nil {
		a.writeError(w, r, http.StatusInternalServerError, "failed to read the latest checks", err)
		return
	}
	a.writeJSON(w, r, http.StatusOK, healthResponse{Count: len(results), Results: results})
}

// getHistory serves past results, newest first, optionally narrowed to one app
// and region.
func (a *api) getHistory(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()

	limit := DefaultHistoryLimit
	if raw := query.Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			a.writeError(w, r, http.StatusBadRequest, fmt.Sprintf("limit must be a number, got %q", raw), nil)
			return
		}
		// Out-of-range values clamp rather than fail: a client asking for more
		// than the cap wants as much as it can have, and one asking for zero or
		// less has said nothing useful.
		limit = min(max(parsed, 1), MaxHistoryLimit)
	}

	results, err := a.store.History(r.Context(), query.Get("app"), query.Get("region"), limit)
	if err != nil {
		a.writeError(w, r, http.StatusInternalServerError, "failed to read the check history", err)
		return
	}
	a.writeJSON(w, r, http.StatusOK, healthResponse{Count: len(results), Results: results})
}

// getLive answers as long as the process is running and its goroutines are not
// wedged. It touches nothing, which is the point: a liveness probe that depends
// on the database restarts the pod when the database is the thing that is slow.
func (a *api) getLive(w http.ResponseWriter, r *http.Request) {
	a.writeJSON(w, r, http.StatusOK, map[string]string{"status": "ok"})
}

// getReady answers only once the hub has something to serve.
func (a *api) getReady(w http.ResponseWriter, r *http.Request) {
	if !a.ready() {
		a.writeJSON(w, r, http.StatusServiceUnavailable, map[string]string{
			"status": "unready",
			"reason": "the first mirror check has not finished",
		})
		return
	}
	a.writeJSON(w, r, http.StatusOK, map[string]string{"status": "ok"})
}

// notFound gives an unknown path the same JSON shape as every other error.
func (a *api) notFound(w http.ResponseWriter, r *http.Request) {
	a.writeError(w, r, http.StatusNotFound, fmt.Sprintf("no such endpoint: %s", r.URL.Path), nil)
}

// errorResponse is the body of every error.
type errorResponse struct {
	Error string `json:"error"`
}

// writeError logs the underlying cause and tells the client only the summary,
// because a database error message is of no use to a caller and may name paths.
func (a *api) writeError(w http.ResponseWriter, r *http.Request, code int, message string, cause error) {
	if cause != nil {
		a.log.Error(message, "error", cause.Error(), "path", r.URL.Path, "request_id", requestIDOf(r))
	}
	a.writeJSON(w, r, code, errorResponse{Error: message})
}

// writeJSON encodes a body. The encode happens into a buffer first so a failure
// halfway through cannot leave a 200 with a truncated body on the wire.
func (a *api) writeJSON(w http.ResponseWriter, r *http.Request, code int, body any) {
	encoded, err := json.Marshal(body)
	if err != nil {
		a.log.Error("failed to encode a response", "error", err.Error(), "path", r.URL.Path)
		http.Error(w, `{"error":"failed to encode the response"}`, http.StatusInternalServerError)
		return
	}
	encoded = append(encoded, '\n')

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Length", strconv.Itoa(len(encoded)))
	w.WriteHeader(code)
	// net/http drops the body of a HEAD response itself, so there is no
	// special case here.
	if _, err := w.Write(encoded); err != nil {
		a.log.Warn("failed to write a response", "error", err.Error(), "path", r.URL.Path)
	}
}

// etag hashes a body into a strong entity tag. Half of a sha256 is far more
// than enough to tell two versions of a mirror list apart and keeps the header
// short.
func etag(body []byte) string {
	sum := sha256.Sum256(body)
	return `"` + hex.EncodeToString(sum[:16]) + `"`
}

// matchesETag reports whether an If-None-Match header covers the given tag.
// The header is a comma separated list and may be "*", and RFC 9110 says a
// weak comparison applies, so a W/ prefix on either side is ignored.
func matchesETag(header, tag string) bool {
	header = strings.TrimSpace(header)
	if header == "" {
		return false
	}
	if header == "*" {
		return true
	}
	for _, candidate := range strings.Split(header, ",") {
		if strings.TrimPrefix(strings.TrimSpace(candidate), "W/") == strings.TrimPrefix(tag, "W/") {
			return true
		}
	}
	return false
}

// requestIDHeader carries the id that ties a client's report to a log line.
const requestIDHeader = "X-Request-ID"

// requestIDKey is the context key the id is stashed under.
type requestIDKey struct{}

// withRequestID adopts the caller's request id or mints one, puts it on the
// response and in the context so every log line for the request can carry it.
func withRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimSpace(r.Header.Get(requestIDHeader))
		if id == "" || len(id) > 128 {
			id = newRequestID()
		}
		w.Header().Set(requestIDHeader, id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey{}, id)))
	})
}

// requestIDOf returns the id [withRequestID] assigned, "" outside it.
func requestIDOf(r *http.Request) string {
	id, _ := r.Context().Value(requestIDKey{}).(string)
	return id
}

// newRequestID mints a random id.
func newRequestID() string {
	var buf [8]byte
	// crypto/rand.Read never fails as of Go 1.24; it panics on a broken system
	// rather than returning an error.
	_, _ = rand.Read(buf[:])
	return hex.EncodeToString(buf[:])
}

// statusRecorder remembers the status code and size an handler wrote, which the
// ResponseWriter itself does not expose.
type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (s *statusRecorder) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	// A handler that writes without calling WriteHeader has implicitly sent a
	// 200, and that is the code to record.
	if s.status == 0 {
		s.status = http.StatusOK
	}
	n, err := s.ResponseWriter.Write(b)
	s.bytes += n
	return n, err
}

// withObservability logs and measures every request.
func withObservability(next http.Handler, metrics *Metrics, log *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorder := &statusRecorder{ResponseWriter: w}
		start := time.Now()

		next.ServeHTTP(recorder, r)

		elapsed := time.Since(start)
		if recorder.status == 0 {
			recorder.status = http.StatusOK
		}

		// ServeMux fills in Request.Pattern while routing, so by the time the
		// call above has returned the route is known. Labelling by pattern
		// rather than by path is what keeps the metric's cardinality bounded:
		// an unmatched path must not mint a new series per URL a scanner tries.
		route := r.Pattern
		if route == "" || strings.HasSuffix(route, "/") {
			route = routeUnmatched
		}

		metrics.ObserveHTTP(route, r.Method, recorder.status, elapsed)
		log.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"route", route,
			"status", recorder.status,
			"bytes", recorder.bytes,
			"duration_ms", elapsed.Milliseconds(),
			"request_id", requestIDOf(r),
		)
	})
}

// routeUnmatched is the metric label for a path no route claimed.
const routeUnmatched = "unmatched"

// withRecovery turns a panic in a handler into a 500 rather than a dropped
// connection and a dead process.
func withRecovery(next http.Handler, log *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			recovered := recover()
			if recovered == nil {
				return
			}
			// A client that hangs up mid-response makes net/http panic with
			// this sentinel on purpose; re-panicking lets it do its job.
			if recovered == http.ErrAbortHandler {
				panic(recovered)
			}
			log.Error("handler panicked",
				"panic", fmt.Sprint(recovered),
				"path", r.URL.Path,
				"request_id", requestIDOf(r),
			)
			http.Error(w, `{"error":"internal server error"}`, http.StatusInternalServerError)
		}()
		next.ServeHTTP(w, r)
	})
}
