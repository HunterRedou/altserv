// Package observability provides health checks and a small, dependency-free
// Prometheus metrics registry. It deliberately records only aggregate
// operational data: HTTP method, route pattern, status code, durations, pool
// statistics and counters. It never records a patient id, an email address, a
// query string or a request body (doc 20).
package observability

import (
	"database/sql"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// DurationBuckets are the histogram buckets in seconds. They cover a fast API
// call up to a slow database report.
var DurationBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

type requestKey struct {
	method string
	route  string
	status string
}

type histogramKey struct {
	method string
	route  string
}

type requestSample struct {
	key   requestKey
	value uint64
}

type histogramSample struct {
	key   histogramKey
	value histogram
}

type jobSample struct {
	name  string
	value uint64
}

type histogram struct {
	buckets []uint64
	sum     float64
	count   uint64
}

// Registry holds all process metrics. All methods are safe for concurrent use
// and nil-safe so a test or a tool can run without metrics configured.
type Registry struct {
	mu       sync.Mutex
	requests map[requestKey]uint64
	duration map[histogramKey]*histogram

	inFlight       atomic.Int64
	loginFailures  atomic.Uint64
	sessionsPurged atomic.Uint64
	retentionRuns  atomic.Uint64
	retentionCands atomic.Uint64
	jobErrors      map[string]uint64

	backupAgeSeconds atomic.Int64
	backupSet        atomic.Bool
	backupReadErrors atomic.Uint64

	slowQueries atomic.Int64
	slowSet     atomic.Bool

	poolStats func() sql.DBStats
	startedAt time.Time
}

// NewRegistry creates an empty registry.
func NewRegistry() *Registry {
	return &Registry{
		requests:  make(map[requestKey]uint64),
		duration:  make(map[histogramKey]*histogram),
		jobErrors: make(map[string]uint64),
		startedAt: time.Now(),
	}
}

// SetPoolStats registers the database pool statistics source.
func (r *Registry) SetPoolStats(fn func() sql.DBStats) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.poolStats = fn
}

// IncLoginFailure counts a failed login attempt.
func (r *Registry) IncLoginFailure() {
	if r == nil {
		return
	}
	r.loginFailures.Add(1)
}

// IncSessionsPurged counts expired and revoked sessions removed by the
// maintenance job.
func (r *Registry) IncSessionsPurged(count int) {
	if r == nil || count <= 0 {
		return
	}
	r.sessionsPurged.Add(uint64(count))
}

// IncRetentionReview records one completed retention review and the number of
// candidate rows it reported. It records a count only, never a patient id.
func (r *Registry) IncRetentionReview(candidates int) {
	if r == nil {
		return
	}
	r.retentionRuns.Add(1)
	if candidates > 0 {
		r.retentionCands.Add(uint64(candidates))
	}
}

// IncJobError counts a failed background job.
func (r *Registry) IncJobError(job string) {
	if r == nil {
		return
	}
	if job == "" {
		job = "unknown"
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.jobErrors[job]++
}

// SetBackupAge records the age of the last successful backup.
func (r *Registry) SetBackupAge(age time.Duration) {
	if r == nil {
		return
	}
	if age < 0 {
		age = 0
	}
	r.backupAgeSeconds.Store(int64(age / time.Second))
	r.backupSet.Store(true)
}

// ClearBackupAge removes the backup age metric.
func (r *Registry) ClearBackupAge() {
	if r == nil {
		return
	}
	r.backupSet.Store(false)
}

// IncBackupReadError counts a failed read of the backup marker.
func (r *Registry) IncBackupReadError() {
	if r == nil {
		return
	}
	r.backupReadErrors.Add(1)
}

// SetSlowQueries records how many statements are currently running longer than
// the configured threshold. It is a snapshot from pg_stat_activity, not a
// cumulative counter.
func (r *Registry) SetSlowQueries(count int64) {
	if r == nil {
		return
	}
	if count < 0 {
		count = 0
	}
	r.slowQueries.Store(count)
	r.slowSet.Store(true)
}

// ObserveRequest records one finished HTTP request. route must be a route
// pattern such as "/api/patients/{id}", never a raw path with ids.
func (r *Registry) ObserveRequest(method, route string, status int, duration time.Duration) {
	if r == nil {
		return
	}
	if method == "" {
		method = "UNKNOWN"
	}
	if route == "" {
		route = "unmatched"
	}
	statusLabel := strconv.Itoa(status)

	r.mu.Lock()
	defer r.mu.Unlock()

	r.requests[requestKey{method: method, route: route, status: statusLabel}]++

	key := histogramKey{method: method, route: route}
	h := r.duration[key]
	if h == nil {
		h = &histogram{buckets: make([]uint64, len(DurationBuckets)+1)}
		r.duration[key] = h
	}
	seconds := duration.Seconds()
	for i, bound := range DurationBuckets {
		if seconds <= bound {
			h.buckets[i]++
		}
	}
	h.buckets[len(DurationBuckets)]++
	h.sum += seconds
	h.count++
}

// Middleware records every request with bounded labels. The route pattern is
// read from the request after the mux served it, so no path parameter and no
// query string can become a label.
func (r *Registry) Middleware(next http.Handler) http.Handler {
	if r == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		start := time.Now()
		r.inFlight.Add(1)
		defer r.inFlight.Add(-1)

		recorder := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(recorder, req)

		status := recorder.status
		if status == 0 {
			status = http.StatusOK
		}
		route := routePattern(req.Pattern)
		r.ObserveRequest(req.Method, route, status, time.Since(start))
	})
}

// routePattern returns the route without a leading HTTP method. Go 1.22 stores
// "GET /api/patients/{id}" in Request.Pattern for method-scoped routes; the
// method is already a separate label, so the path pattern is enough.
func routePattern(pattern string) string {
	if pattern == "" {
		return "unmatched"
	}
	if index := strings.IndexByte(pattern, ' '); index >= 0 {
		return pattern[index+1:]
	}
	return pattern
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	if r.status == 0 {
		r.status = code
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(body []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.ResponseWriter.Write(body)
}

func (r *statusRecorder) Unwrap() http.ResponseWriter {
	return r.ResponseWriter
}

func (r *statusRecorder) Flush() {
	if flusher, ok := r.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// Handler renders the registry in the Prometheus text exposition format.
func (r *Registry) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(r.Render()))
	})
}

// Render returns the current metrics as Prometheus text.
func (r *Registry) Render() string {
	if r == nil {
		return ""
	}

	r.mu.Lock()
	requests := make([]requestSample, 0, len(r.requests))
	for key, value := range r.requests {
		requests = append(requests, requestSample{key: key, value: value})
	}
	sort.Slice(requests, func(i, j int) bool {
		if requests[i].key.method != requests[j].key.method {
			return requests[i].key.method < requests[j].key.method
		}
		if requests[i].key.route != requests[j].key.route {
			return requests[i].key.route < requests[j].key.route
		}
		return requests[i].key.status < requests[j].key.status
	})
	histograms := make([]histogramSample, 0, len(r.duration))
	for key, value := range r.duration {
		copied := histogram{
			buckets: append([]uint64(nil), value.buckets...),
			sum:     value.sum,
			count:   value.count,
		}
		histograms = append(histograms, histogramSample{key: key, value: copied})
	}
	sort.Slice(histograms, func(i, j int) bool {
		if histograms[i].key.method != histograms[j].key.method {
			return histograms[i].key.method < histograms[j].key.method
		}
		return histograms[i].key.route < histograms[j].key.route
	})
	jobs := make([]jobSample, 0, len(r.jobErrors))
	for name, value := range r.jobErrors {
		jobs = append(jobs, jobSample{name: name, value: value})
	}
	sort.Slice(jobs, func(i, j int) bool {
		return jobs[i].name < jobs[j].name
	})
	poolStats := r.poolStats
	startedAt := r.startedAt
	r.mu.Unlock()

	var b strings.Builder

	b.WriteString("# HELP altserv_http_requests_total Total HTTP requests by method, route pattern and status.\n")
	b.WriteString("# TYPE altserv_http_requests_total counter\n")
	for _, sample := range requests {
		fmt.Fprintf(&b, "altserv_http_requests_total{method=%q,route=%q,status=%q} %d\n",
			EscapeLabel(sample.key.method), EscapeLabel(sample.key.route), EscapeLabel(sample.key.status), sample.value)
	}

	b.WriteString("# HELP altserv_http_request_duration_seconds HTTP request latency in seconds.\n")
	b.WriteString("# TYPE altserv_http_request_duration_seconds histogram\n")
	for _, sample := range histograms {
		h := sample.value
		labels := fmt.Sprintf("method=%q,route=%q", EscapeLabel(sample.key.method), EscapeLabel(sample.key.route))
		for i, bound := range DurationBuckets {
			fmt.Fprintf(&b, "altserv_http_request_duration_seconds_bucket{%s,le=%q} %d\n",
				labels, strconv.FormatFloat(bound, 'g', -1, 64), h.buckets[i])
		}
		fmt.Fprintf(&b, "altserv_http_request_duration_seconds_bucket{%s,le=\"+Inf\"} %d\n", labels, h.buckets[len(DurationBuckets)])
		fmt.Fprintf(&b, "altserv_http_request_duration_seconds_sum{%s} %s\n", labels, formatFloat(h.sum))
		fmt.Fprintf(&b, "altserv_http_request_duration_seconds_count{%s} %d\n", labels, h.count)
	}

	b.WriteString("# HELP altserv_http_in_flight Current HTTP requests being served.\n")
	b.WriteString("# TYPE altserv_http_in_flight gauge\n")
	fmt.Fprintf(&b, "altserv_http_in_flight %d\n", r.inFlight.Load())

	b.WriteString("# HELP altserv_login_failures_total Failed login attempts.\n")
	b.WriteString("# TYPE altserv_login_failures_total counter\n")
	fmt.Fprintf(&b, "altserv_login_failures_total %d\n", r.loginFailures.Load())

	b.WriteString("# HELP altserv_sessions_purged_total Expired and revoked sessions removed by the maintenance job.\n")
	b.WriteString("# TYPE altserv_sessions_purged_total counter\n")
	fmt.Fprintf(&b, "altserv_sessions_purged_total %d\n", r.sessionsPurged.Load())

	b.WriteString("# HELP altserv_retention_reviews_total Completed retention reviews.\n")
	b.WriteString("# TYPE altserv_retention_reviews_total counter\n")
	fmt.Fprintf(&b, "altserv_retention_reviews_total %d\n", r.retentionRuns.Load())

	b.WriteString("# HELP altserv_retention_review_candidates_total Retention review candidate rows reported.\n")
	b.WriteString("# TYPE altserv_retention_review_candidates_total counter\n")
	fmt.Fprintf(&b, "altserv_retention_review_candidates_total %d\n", r.retentionCands.Load())

	b.WriteString("# HELP altserv_job_errors_total Failed background jobs.\n")
	b.WriteString("# TYPE altserv_job_errors_total counter\n")
	for _, sample := range jobs {
		fmt.Fprintf(&b, "altserv_job_errors_total{job=%q} %d\n", EscapeLabel(sample.name), sample.value)
	}

	b.WriteString("# HELP altserv_process_start_time_seconds Unix start time of the process.\n")
	b.WriteString("# TYPE altserv_process_start_time_seconds gauge\n")
	fmt.Fprintf(&b, "altserv_process_start_time_seconds %d\n", startedAt.Unix())

	if r.backupSet.Load() {
		b.WriteString("# HELP altserv_backup_age_seconds Age of the last successful backup marker in seconds.\n")
		b.WriteString("# TYPE altserv_backup_age_seconds gauge\n")
		fmt.Fprintf(&b, "altserv_backup_age_seconds %d\n", r.backupAgeSeconds.Load())
	}
	b.WriteString("# HELP altserv_backup_marker_read_errors_total Failed reads of the backup marker.\n")
	b.WriteString("# TYPE altserv_backup_marker_read_errors_total counter\n")
	fmt.Fprintf(&b, "altserv_backup_marker_read_errors_total %d\n", r.backupReadErrors.Load())

	if r.slowSet.Load() {
		b.WriteString("# HELP altserv_db_slow_queries Statements currently running longer than the slow-query threshold.\n")
		b.WriteString("# TYPE altserv_db_slow_queries gauge\n")
		fmt.Fprintf(&b, "altserv_db_slow_queries %d\n", r.slowQueries.Load())
	}

	if poolStats != nil {
		stats := poolStats()
		writeGauge(&b, "altserv_db_pool_max_open_connections", "Configured maximum open database connections.", float64(stats.MaxOpenConnections))
		writeGauge(&b, "altserv_db_pool_open_connections", "Open database connections.", float64(stats.OpenConnections))
		writeGauge(&b, "altserv_db_pool_in_use", "Database connections in use.", float64(stats.InUse))
		writeGauge(&b, "altserv_db_pool_idle", "Idle database connections.", float64(stats.Idle))
		writeCounter(&b, "altserv_db_pool_wait_count_total", "Total waits for a database connection.", float64(stats.WaitCount))
		writeCounter(&b, "altserv_db_pool_wait_duration_seconds_total", "Total time spent waiting for a database connection.", stats.WaitDuration.Seconds())
	}

	return b.String()
}

func writeGauge(b *strings.Builder, name, help string, value float64) {
	fmt.Fprintf(b, "# HELP %s %s\n", name, help)
	fmt.Fprintf(b, "# TYPE %s gauge\n", name)
	fmt.Fprintf(b, "%s %s\n", name, formatFloat(value))
}

func writeCounter(b *strings.Builder, name, help string, value float64) {
	fmt.Fprintf(b, "# HELP %s %s\n", name, help)
	fmt.Fprintf(b, "# TYPE %s counter\n", name)
	fmt.Fprintf(b, "%s %s\n", name, formatFloat(value))
}

func formatFloat(value float64) string {
	return strconv.FormatFloat(value, 'g', -1, 64)
}

// EscapeLabel escapes a Prometheus label value.
func EscapeLabel(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, "\n", `\n`)
	value = strings.ReplaceAll(value, `"`, `\"`)
	return value
}
