// Command loadgen is a small read-only load generator for altserv. It exists so
// an operator can repeat the load test from docs/runbooks/load-testing.md when
// k6 is not available.
//
// It logs in once, then sends GET requests with a fixed concurrency for a fixed
// duration and prints the latency percentiles, the request rate, the error rate
// and the status distribution. It never writes data.
//
// Usage:
//
//	go run ./cmd/loadgen \
//	  -base http://127.0.0.1:8080 \
//	  -email loadtest@example.invalid -password '...' \
//	  -paths /api/me,/api/patients,/healthz,/readyz \
//	  -c 10 -d 20s
//
// Use a dedicated read-only employee account and synthetic patients. Never run
// it against production with a real chef account.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const sessionCookieName = "__Host-session"

type loginResponse struct {
	CSRFToken string `json:"csrf_token"`
}

type result struct {
	status    int
	latency   time.Duration
	transport bool
}

func main() {
	base := flag.String("base", "http://127.0.0.1:8080", "base URL of the server")
	email := flag.String("email", "", "login email (required for /api paths)")
	password := flag.String("password", "", "login password")
	pathsFlag := flag.String("paths", "/api/me,/api/patients,/healthz,/readyz", "comma-separated GET paths")
	concurrency := flag.Int("c", 10, "concurrent workers")
	duration := flag.Duration("d", 20*time.Second, "measurement duration")
	warmup := flag.Duration("warmup", 2*time.Second, "warmup duration, not measured")
	maxErrorRate := flag.Float64("max-error-rate", 0.01, "fail when the error rate is at or above this value")
	maxP95 := flag.Duration("p95", 500*time.Millisecond, "fail when p95 is at or above this value")
	maxP99 := flag.Duration("p99", 1500*time.Millisecond, "fail when p99 is at or above this value")
	flag.Parse()

	if *concurrency < 1 {
		fatalf("-c must be at least 1")
	}
	if *duration <= 0 {
		fatalf("-d must be positive")
	}

	paths := splitPaths(*pathsFlag)
	if len(paths) == 0 {
		fatalf("-paths must contain at least one path")
	}
	if *email == "" && hasAPIPath(paths) {
		fatalf("-email and -password are required for /api paths")
	}

	client := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        *concurrency * 2,
			MaxIdleConnsPerHost: *concurrency * 2,
			IdleConnTimeout:     30 * time.Second,
			DisableCompression:  true,
		},
	}

	cookie, csrf := "", ""
	if *email != "" {
		var err error
		cookie, csrf, err = login(client, *base, *email, *password)
		if err != nil {
			fatalf("login: %v", err)
		}
	}

	fmt.Printf("base:        %s\n", *base)
	fmt.Printf("paths:       %s\n", strings.Join(paths, ", "))
	fmt.Printf("concurrency: %d\n", *concurrency)
	fmt.Printf("warmup:      %s\n", *warmup)
	fmt.Printf("duration:    %s\n", *duration)

	if *warmup > 0 {
		run(*base, paths, cookie, csrf, client, *concurrency, *warmup)
		fmt.Println("warmup complete")
	}

	started := time.Now()
	latencies, statuses, transportErrors := run(*base, paths, cookie, csrf, client, *concurrency, *duration)
	elapsed := time.Since(started)

	if len(latencies) == 0 {
		fatalf("no requests completed")
	}
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })

	total := len(latencies)
	errorCount := 0
	for status, count := range statuses {
		if status >= 400 {
			errorCount += count
		}
	}
	errorCount += transportErrors
	errorRate := float64(errorCount) / float64(total)

	p := func(percentile float64) time.Duration {
		index := int(float64(total-1) * percentile)
		return latencies[index]
	}

	fmt.Println()
	fmt.Printf("requests:    %d\n", total)
	fmt.Printf("rps:         %.1f\n", float64(total)/elapsed.Seconds())
	fmt.Printf("errors:      %d (%.2f%%)\n", errorCount, errorRate*100)
	fmt.Printf("latency p50: %s\n", p(0.50))
	fmt.Printf("latency p95: %s\n", p(0.95))
	fmt.Printf("latency p99: %s\n", p(0.99))
	fmt.Printf("latency max: %s\n", latencies[total-1])
	fmt.Print("status:     ")
	keys := make([]int, 0, len(statuses))
	for status := range statuses {
		keys = append(keys, status)
	}
	sort.Ints(keys)
	for _, status := range keys {
		fmt.Printf(" %d=%d", status, statuses[status])
	}
	if transportErrors > 0 {
		fmt.Printf(" transport_error=%d", transportErrors)
	}
	fmt.Println()

	failed := false
	if errorRate >= *maxErrorRate {
		fmt.Printf("FAIL: error rate %.2f%% >= %.2f%%\n", errorRate*100, *maxErrorRate*100)
		failed = true
	}
	if p95 := p(0.95); p95 >= *maxP95 {
		fmt.Printf("FAIL: p95 %s >= %s\n", p95, *maxP95)
		failed = true
	}
	if p99 := p(0.99); p99 >= *maxP99 {
		fmt.Printf("FAIL: p99 %s >= %s\n", p99, *maxP99)
		failed = true
	}
	if failed {
		os.Exit(1)
	}
	fmt.Println("PASS: thresholds met")
}

func splitPaths(raw string) []string {
	parts := strings.Split(raw, ",")
	paths := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			paths = append(paths, part)
		}
	}
	return paths
}

func hasAPIPath(paths []string) bool {
	for _, path := range paths {
		if strings.HasPrefix(path, "/api/") {
			return true
		}
	}
	return false
}

func login(client *http.Client, base, email, password string) (string, string, error) {
	body, err := json.Marshal(map[string]string{"email": email, "password": password})
	if err != nil {
		return "", "", err
	}

	request, err := http.NewRequest(http.MethodPost, strings.TrimRight(base, "/")+"/api/login", bytes.NewReader(body))
	if err != nil {
		return "", "", err
	}
	request.Header.Set("Content-Type", "application/json")

	response, err := client.Do(request)
	if err != nil {
		return "", "", err
	}
	defer response.Body.Close()

	payload, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return "", "", err
	}
	if response.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("status %d: %s", response.StatusCode, strings.TrimSpace(string(payload)))
	}

	var parsed loginResponse
	if err := json.Unmarshal(payload, &parsed); err != nil {
		return "", "", fmt.Errorf("decode login response: %w", err)
	}
	if parsed.CSRFToken == "" {
		return "", "", fmt.Errorf("login response has no csrf_token")
	}

	cookie := ""
	for _, c := range response.Cookies() {
		if c.Name == sessionCookieName {
			cookie = c.Value
			break
		}
	}
	if cookie == "" {
		return "", "", fmt.Errorf("login response has no %s cookie", sessionCookieName)
	}
	return cookie, parsed.CSRFToken, nil
}

func run(base string, paths []string, cookie, csrf string, client *http.Client, concurrency int, duration time.Duration) ([]time.Duration, map[int]int, int) {
	deadline := time.Now().Add(duration)

	var total atomic.Int64
	var transportErrors atomic.Int64

	statuses := make(map[int]int)
	var statusMu sync.Mutex

	perWorker := make([][]time.Duration, concurrency)
	var wg sync.WaitGroup

	for worker := 0; worker < concurrency; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()

			results := make([]result, 0, 1024)
			index := worker
			for time.Now().Before(deadline) {
				path := paths[index%len(paths)]
				index++

				latency, status, transportError := request(client, strings.TrimRight(base, "/")+path, cookie, csrf)
				results = append(results, result{status: status, latency: latency, transport: transportError})
				total.Add(1)
			}

			latencies := make([]time.Duration, 0, len(results))
			for _, item := range results {
				if item.transport {
					transportErrors.Add(1)
					continue
				}
				latencies = append(latencies, item.latency)
				statusMu.Lock()
				statuses[item.status]++
				statusMu.Unlock()
			}
			perWorker[worker] = latencies
		}(worker)
	}
	wg.Wait()

	latencies := make([]time.Duration, 0, total.Load())
	for _, workerLatencies := range perWorker {
		latencies = append(latencies, workerLatencies...)
	}
	return latencies, statuses, int(transportErrors.Load())
}

func request(client *http.Client, url, cookie, csrf string) (time.Duration, int, bool) {
	request, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return 0, 0, true
	}
	if cookie != "" {
		request.Header.Set("Cookie", sessionCookieName+"="+cookie)
	}
	if csrf != "" {
		request.Header.Set("X-CSRF-Token", csrf)
	}

	started := time.Now()
	response, err := client.Do(request)
	latency := time.Since(started)
	if err != nil {
		return latency, 0, true
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<20))
	return latency, response.StatusCode, false
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "loadgen: "+format+"\n", args...)
	os.Exit(2)
}
