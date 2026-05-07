// sentry-replay reads a Sentry event, fires the corresponding HTTP
// request at a target app, and writes a Keploy-format testcase YAML
// containing both the request and the captured response.
//
// This mirrors the pattern in keploy's pkg/service/import/import.go
// (PostmanImporter.sendRequest) — same idea, different source.
//
// When run alongside `keploy record`, the proxy intercepts every outgoing
// call the app makes during this request and stores them as mocks
// alongside the testcase. Keploy then has a fully-replayable test.
//
// Usage:
//   sentry-replay --event event.json --base-url http://localhost:8080 --out tc.yaml
//   sentry-replay --event event.json --base-url ... --bearer-from-env API_TOKEN
//
// The output testcase YAML can be dropped into a keploy test-set
// directory and run via `keploy test --testcase <name>`.
package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/AkashKumar7902/keploy-sentry-poc/internal/keploycase"
	"github.com/AkashKumar7902/keploy-sentry-poc/internal/sentry"
)

var flakyHeadersToStrip = map[string]bool{
	"x-amz-date":           true,
	"x-amz-security-token": true,
	"x-amz-content-sha256": true,
	"x-amz-signature":      true,
	"x-amzn-trace-id":      true,
	"traceparent":          true,
	"tracestate":           true,
	"x-b3-traceid":         true,
	"x-b3-spanid":          true,
	"x-datadog-trace-id":   true,
	"x-datadog-parent-id":  true,
	"x-request-id":         true,
	"x-correlation-id":     true,
	"request-id":           true,
	"host":                 true, // gets reset by http.Client based on URL
	"content-length":       true, // recomputed on send
}

type config struct {
	eventPath     string
	baseURL       string
	bearer        string
	bearerFromEnv string
	out           string
	dryRun        bool
	includeStack  bool
	includeRel    bool
	timeout       time.Duration
	insecure      bool
}

func main() {
	cfg := parseFlags()

	evt, err := loadEvent(cfg.eventPath)
	if err != nil {
		die("load event: %v", err)
	}

	req, err := buildHTTPRequest(evt, cfg)
	if err != nil {
		die("build request: %v", err)
	}

	if cfg.dryRun {
		fmt.Fprintln(os.Stderr, "[dry-run] would fire:", req.Method, req.URL.String())
		fmt.Fprintln(os.Stderr, "[dry-run] headers:", req.Header)
		return
	}

	resp, body, err := fire(req, cfg.timeout)
	if err != nil {
		die("fire request: %v", err)
	}

	tc := buildTestCase(evt, req, resp, body, cfg)

	w := os.Stdout
	if cfg.out != "" && cfg.out != "-" {
		f, err := os.Create(cfg.out)
		if err != nil {
			die("create output: %v", err)
		}
		defer f.Close()
		w = f
	}
	if err := tc.WriteYAML(w); err != nil {
		die("write yaml: %v", err)
	}

	fmt.Fprintf(os.Stderr, "captured: %s %s -> %d  (testcase: %s)\n",
		req.Method, req.URL.String(), resp.StatusCode, tc.Name)
}

func parseFlags() config {
	cfg := config{
		timeout:      30 * time.Second,
		includeStack: true,
		includeRel:   true,
	}
	flag.StringVar(&cfg.eventPath, "event", "", "path to Sentry event JSON (required)")
	flag.StringVar(&cfg.baseURL, "base-url", "", "target app base URL (overrides Sentry URL host); required if Sentry URL is not reachable")
	flag.StringVar(&cfg.bearer, "bearer", "", "bearer token to inject")
	flag.StringVar(&cfg.bearerFromEnv, "bearer-from-env", "", "env var name to read bearer from")
	flag.StringVar(&cfg.out, "out", "-", "output path for testcase YAML (- for stdout)")
	flag.BoolVar(&cfg.dryRun, "dry-run", false, "build but do not fire the request")
	flag.BoolVar(&cfg.includeStack, "include-stacktrace", cfg.includeStack, "include exception/stack info in testcase description")
	flag.BoolVar(&cfg.includeRel, "include-release", cfg.includeRel, "include Sentry release in testcase description")
	flag.DurationVar(&cfg.timeout, "timeout", cfg.timeout, "HTTP timeout")
	flag.BoolVar(&cfg.insecure, "insecure", false, "skip TLS verification (testing only)")
	flag.Parse()

	if cfg.eventPath == "" {
		die("--event is required")
	}
	return cfg
}

func loadEvent(path string) (*sentry.Event, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return sentry.Parse(f)
}

func buildHTTPRequest(evt *sentry.Event, cfg config) (*http.Request, error) {
	url := evt.Request.URL
	if cfg.baseURL != "" {
		path, q, _, _, err := evt.Request.SplitURL()
		if err != nil {
			return nil, err
		}
		url = strings.TrimRight(cfg.baseURL, "/") + path
		if q != "" {
			url += "?" + q
		}
	}

	body := evt.Request.BodyString()
	req, err := http.NewRequestWithContext(context.Background(), evt.Request.Method, url, strings.NewReader(body))
	if err != nil {
		return nil, err
	}

	for k, v := range evt.Request.Headers {
		if flakyHeadersToStrip[strings.ToLower(k)] {
			continue
		}
		req.Header.Set(k, v)
	}
	if tok := resolveBearer(cfg.bearer, cfg.bearerFromEnv); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	return req, nil
}

func fire(req *http.Request, timeout time.Duration) (*http.Response, string, error) {
	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, resp.Body); err != nil {
		return nil, "", err
	}
	return resp, buf.String(), nil
}

func buildTestCase(evt *sentry.Event, req *http.Request, resp *http.Response, body string, cfg config) *keploycase.TestCase {
	hreq := keploycase.HTTPReq{
		Method:     req.Method,
		ProtoMajor: 1,
		ProtoMinor: 1,
		URL:        req.URL.RequestURI(),
		URLParams:  flattenQuery(req.URL.Query()),
		Header:     flattenHeader(req.Header),
		Body:       evt.Request.BodyString(),
		Timestamp:  time.Now().UTC(),
	}
	hresp := keploycase.HTTPResp{
		StatusCode:    resp.StatusCode,
		Header:        flattenHeader(resp.Header),
		Body:          body,
		StatusMessage: http.StatusText(resp.StatusCode),
		ProtoMajor:    1,
		ProtoMinor:    1,
	}

	desc := buildDescription(evt, cfg)
	name := fmt.Sprintf("sentry-%s", safeID(evt.EventID))
	return keploycase.New(name, desc, hreq, hresp)
}

func buildDescription(evt *sentry.Event, cfg config) string {
	parts := []string{fmt.Sprintf("Captured from sentry event %s", evt.EventID)}
	if cfg.includeRel && evt.Release != "" {
		parts = append(parts, fmt.Sprintf("release=%s", evt.Release))
	}
	if cfg.includeStack && len(evt.Exception.Values) > 0 {
		ev := evt.Exception.Values[0]
		parts = append(parts, fmt.Sprintf("exc=%s: %s", ev.Type, truncate(ev.Value, 120)))
		if len(ev.Stacktrace.Frames) > 0 {
			top := ev.Stacktrace.Frames[len(ev.Stacktrace.Frames)-1]
			parts = append(parts, fmt.Sprintf("at=%s:%d", top.Filename, top.Lineno))
		}
	}
	if evt.Contexts.Response.StatusCode != 0 {
		parts = append(parts, fmt.Sprintf("prod-status=%d", evt.Contexts.Response.StatusCode))
	}
	return strings.Join(parts, " | ")
}

func resolveBearer(direct, fromEnv string) string {
	if direct != "" {
		return direct
	}
	if fromEnv != "" {
		return os.Getenv(fromEnv)
	}
	return ""
}

func flattenHeader(h http.Header) map[string]string {
	out := map[string]string{}
	for k, v := range h {
		out[k] = strings.Join(v, ",")
	}
	return out
}

func flattenQuery(q map[string][]string) map[string]string {
	out := map[string]string{}
	for k, v := range q {
		if len(v) > 0 {
			out[k] = v[0]
		}
	}
	return out
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

func safeID(s string) string {
	if s == "" {
		return "unknown"
	}
	r := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_':
			r = append(r, c)
		}
	}
	if len(r) > 16 {
		r = r[:16]
	}
	return string(r)
}

func die(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "sentry-replay: "+format+"\n", args...)
	os.Exit(1)
}
