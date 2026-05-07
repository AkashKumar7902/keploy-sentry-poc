// sentry-to-curl reads a Sentry event JSON and prints a runnable curl
// command. Useful when a developer wants to manually reproduce a prod
// failure: `keploy record` against the app, then run the printed curl.
//
// Sentry strips Authorization by default; pass --bearer or
// --bearer-from-env to inject a working token.
//
// Usage:
//   sentry-to-curl < event.json
//   sentry-to-curl --event event.json --bearer "$TOKEN"
//   sentry-to-curl --event event.json --base-url http://localhost:8080
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/AkashKumar7902/keploy-sentry-poc/internal/sentry"
)

// flakyHeadersToStrip drops Sentry-injected noise headers from the
// reconstructed request so the new testcase doesn't permanently embed
// expired tokens, stale trace IDs, or per-request UUIDs. Mirrors the
// flakyHeaders list in keploy's pkg/agent/proxy/integrations/http/match.go.
var flakyHeadersToStrip = map[string]bool{
	"x-amz-date":          true,
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
}

func main() {
	var (
		eventPath     = flag.String("event", "", "path to Sentry event JSON (default: stdin)")
		bearer        = flag.String("bearer", "", "bearer token to inject as Authorization header")
		bearerFromEnv = flag.String("bearer-from-env", "", "env var name to read bearer token from")
		baseURL       = flag.String("base-url", "", "override scheme+host of the URL (e.g. http://localhost:8080)")
		keepNoise     = flag.Bool("keep-noisy-headers", false, "keep tracing/auth/request-id headers (default strips them)")
	)
	flag.Parse()

	in := os.Stdin
	if *eventPath != "" {
		f, err := os.Open(*eventPath)
		if err != nil {
			die("open event: %v", err)
		}
		defer f.Close()
		in = f
	}

	evt, err := sentry.Parse(in)
	if err != nil {
		die("parse event: %v", err)
	}

	url := evt.Request.URL
	if *baseURL != "" {
		path, q, _, _, perr := evt.Request.SplitURL()
		if perr != nil {
			die("split url: %v", perr)
		}
		url = strings.TrimRight(*baseURL, "/") + path
		if q != "" {
			url += "?" + q
		}
	}

	headers := map[string]string{}
	for k, v := range evt.Request.Headers {
		if !*keepNoise && flakyHeadersToStrip[strings.ToLower(k)] {
			continue
		}
		headers[k] = v
	}
	if tok := resolveBearer(*bearer, *bearerFromEnv); tok != "" {
		headers["Authorization"] = "Bearer " + tok
	}

	fmt.Println(buildCurl(evt.Request.Method, url, headers, evt.Request.BodyString()))
	fmt.Fprintf(os.Stderr, "\n# provenance: %s\n", evt.Provenance())
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

func buildCurl(method, url string, headers map[string]string, body string) string {
	out := fmt.Sprintf("curl --request %s \\\n  --url %s", method, url)
	for k, v := range headers {
		if strings.EqualFold(k, "Content-Length") {
			continue
		}
		out += fmt.Sprintf(" \\\n  --header '%s: %s'", k, v)
	}
	if body != "" {
		out += fmt.Sprintf(" \\\n  --data %q", body)
	}
	return out
}

func die(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "sentry-to-curl: "+format+"\n", args...)
	os.Exit(1)
}
