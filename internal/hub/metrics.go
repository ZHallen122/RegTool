package hub

import (
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
)

// Metrics is every Prometheus series the hub exports.
//
// It owns a dedicated [prometheus.Registry] rather than using the package-level
// default one. Registering the same collector twice panics, so a shared default
// registry would make it impossible to stand up two hubs in one process — which
// is exactly what the tests do.
type Metrics struct {
	registry *prometheus.Registry

	mirrorUp      *prometheus.GaugeVec
	mirrorLatency *prometheus.GaugeVec
	checksTotal   *prometheus.CounterVec
	checkDuration prometheus.Histogram

	httpRequests *prometheus.CounterVec
	httpDuration *prometheus.HistogramVec
}

// NewMetrics builds the metric set and registers it, along with the Go runtime
// and process collectors, on a registry of its own.
func NewMetrics() *Metrics {
	metrics := &Metrics{
		registry: prometheus.NewRegistry(),

		mirrorUp: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "regtool_hub_mirror_up",
			Help: "Whether the mirror answered its last health check: 1 for up, 0 for down.",
		}, []string{"app", "region", "url"}),

		mirrorLatency: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "regtool_hub_mirror_latency_seconds",
			Help: "Round trip time of the mirror's last health check, in seconds.",
		}, []string{"app", "region", "url"}),

		checksTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "regtool_hub_checks_total",
			Help: "Mirror health checks performed, by outcome.",
		}, []string{"result"}),

		checkDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "regtool_hub_check_duration_seconds",
			Help:    "Wall clock time of a full check run over every mirror, in seconds.",
			Buckets: prometheus.ExponentialBuckets(0.05, 2, 10),
		}),

		httpRequests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "regtool_hub_http_requests_total",
			Help: "HTTP requests served, by route, method and status code.",
		}, []string{"route", "method", "code"}),

		httpDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "regtool_hub_http_request_duration_seconds",
			Help:    "Time spent serving an HTTP request, in seconds.",
			Buckets: prometheus.DefBuckets,
		}, []string{"route"}),
	}

	metrics.registry.MustRegister(
		metrics.mirrorUp,
		metrics.mirrorLatency,
		metrics.checksTotal,
		metrics.checkDuration,
		metrics.httpRequests,
		metrics.httpDuration,
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)

	// Both outcomes exist from the start, so a dashboard shows a zero rather
	// than a gap before the first failure.
	metrics.checksTotal.WithLabelValues(resultLabelOK)
	metrics.checksTotal.WithLabelValues(resultLabelError)

	return metrics
}

// Outcome labels of regtool_hub_checks_total.
const (
	resultLabelOK    = "ok"
	resultLabelError = "error"
)

// Registry is the registry to serve /metrics from.
func (m *Metrics) Registry() *prometheus.Registry { return m.registry }

// ObserveResults records one check run: a gauge pair per mirror and a counter
// bump per result.
func (m *Metrics) ObserveResults(results []Result) {
	for _, result := range results {
		labels := prometheus.Labels{"app": result.App, "region": result.Region, "url": result.URL}

		up := 0.0
		outcome := resultLabelError
		if result.OK {
			up, outcome = 1, resultLabelOK
		}
		m.mirrorUp.With(labels).Set(up)
		m.mirrorLatency.With(labels).Set((time.Duration(result.LatencyMS) * time.Millisecond).Seconds())
		m.checksTotal.WithLabelValues(outcome).Inc()
	}
}

// ObserveCheckRun records how long a full run over every mirror took.
func (m *Metrics) ObserveCheckRun(d time.Duration) {
	m.checkDuration.Observe(d.Seconds())
}

// ObserveHTTP records one served request.
func (m *Metrics) ObserveHTTP(route, method string, code int, d time.Duration) {
	m.httpRequests.WithLabelValues(route, method, strconv.Itoa(code)).Inc()
	m.httpDuration.WithLabelValues(route).Observe(d.Seconds())
}
