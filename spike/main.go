// Spike: validate the Keploy x Sentry shape-matching idea.
//
// Given a Sentry event (with method/url/headers) and a set of Keploy
// testcases (HTTPReq), compute a deterministic signature for each using
// rules that mirror pkg/agent/proxy/integrations/http/match.go (header
// flaky-list, key-only matching, content-type media-type comparison).
//
// Then report which testcases match in three tiers:
//   T1 — exact signature (method + path + sorted query keys + sorted
//        non-flaky header keys + content-type media type)
//   T2 — method + path + sorted query keys
//   T3 — method + path
//
// Run: cd sentry-spike && go run .
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"mime"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// flakyHeaders is a trimmed mirror of the list in
// pkg/agent/proxy/integrations/http/match.go. Lowercased.
var flakyHeaders = map[string]bool{
	"authorization":       true,
	"x-amz-date":          true,
	"x-amz-security-token": true,
	"x-amz-content-sha256": true,
	"x-amz-signature":      true,
	"x-amzn-trace-id":      true,
	"date":                 true,
	"traceparent":          true,
	"tracestate":           true,
	"x-b3-traceid":         true,
	"x-b3-spanid":          true,
	"x-datadog-trace-id":   true,
	"x-datadog-parent-id":  true,
	"x-request-id":         true,
	"x-correlation-id":     true,
	"request-id":           true,
	"stripe-signature":     true,
	"x-hub-signature-256":  true,
	"idempotency-key":      true,
	"x-csrf-token":         true,
}

type sentryEvent struct {
	EventID string `json:"event_id"`
	Request struct {
		Method  string            `json:"method"`
		URL     string            `json:"url"`
		Headers map[string]string `json:"headers"`
	} `json:"request"`
	Contexts struct {
		Response struct {
			StatusCode int `json:"status_code"`
		} `json:"response"`
	} `json:"contexts"`
}

type httpReq struct {
	Method    string            `yaml:"method"`
	URL       string            `yaml:"url"`
	URLParams map[string]string `yaml:"url_params"`
	Header    map[string]string `yaml:"header"`
	Body      string            `yaml:"body"`
}

type testCase struct {
	Name    string  `yaml:"name"`
	HTTPReq httpReq `yaml:"http_req"`
}

// canonical describes the parts of a request that participate in matching.
type canonical struct {
	method      string
	path        string
	queryKeys   []string
	headerKeys  []string
	contentType string
}

func canonicalize(method, urlStr string, headers map[string]string) canonical {
	c := canonical{method: strings.ToUpper(strings.TrimSpace(method))}

	if u, err := url.Parse(urlStr); err == nil {
		c.path = u.Path
		qkeys := map[string]struct{}{}
		for k := range u.Query() {
			qkeys[k] = struct{}{}
		}
		for k := range qkeys {
			c.queryKeys = append(c.queryKeys, k)
		}
	} else {
		c.path = urlStr
	}
	sort.Strings(c.queryKeys)

	hkeys := map[string]struct{}{}
	for k, v := range headers {
		lk := strings.ToLower(strings.TrimSpace(k))
		if flakyHeaders[lk] {
			continue
		}
		if lk == "content-type" {
			if mt, _, err := mime.ParseMediaType(v); err == nil {
				c.contentType = mt
			} else {
				c.contentType = strings.TrimSpace(v)
			}
			continue
		}
		hkeys[lk] = struct{}{}
	}
	for k := range hkeys {
		c.headerKeys = append(c.headerKeys, k)
	}
	sort.Strings(c.headerKeys)

	return c
}

// canonicalizeFromURLParams handles the Keploy testcase shape, where
// URLParams is stored alongside URL. The URL field already contains the
// query string, so url.Parse picks it up — URLParams is a redundant
// convenience map and we just verify by merging.
func canonicalizeFromTestCase(tc testCase) canonical {
	c := canonicalize(tc.HTTPReq.Method, tc.HTTPReq.URL, tc.HTTPReq.Header)
	// merge URLParams keys in case URL was stored without query string
	if len(c.queryKeys) == 0 && len(tc.HTTPReq.URLParams) > 0 {
		seen := map[string]struct{}{}
		for k := range tc.HTTPReq.URLParams {
			seen[k] = struct{}{}
		}
		c.queryKeys = c.queryKeys[:0]
		for k := range seen {
			c.queryKeys = append(c.queryKeys, k)
		}
		sort.Strings(c.queryKeys)
	}
	return c
}

func (c canonical) tier1() string {
	parts := []string{
		"v1",
		c.method,
		c.path,
		"q:" + strings.Join(c.queryKeys, ","),
		"h:" + strings.Join(c.headerKeys, ","),
		"ct:" + c.contentType,
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return hex.EncodeToString(sum[:8])
}

func (c canonical) tier2() string {
	return c.method + " " + c.path + " q:" + strings.Join(c.queryKeys, ",")
}

func (c canonical) tier3() string {
	return c.method + " " + c.path
}

func loadTestCases(dir string) ([]testCase, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []testCase
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "testcase-") || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		var tc testCase
		if err := yaml.Unmarshal(b, &tc); err != nil {
			return nil, fmt.Errorf("%s: %w", e.Name(), err)
		}
		out = append(out, tc)
	}
	return out, nil
}

func main() {
	root := "testdata"

	evtBytes, err := os.ReadFile(filepath.Join(root, "sentry-event.json"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "read sentry event:", err)
		os.Exit(1)
	}
	var evt sentryEvent
	if err := json.Unmarshal(evtBytes, &evt); err != nil {
		fmt.Fprintln(os.Stderr, "parse sentry event:", err)
		os.Exit(1)
	}

	cases, err := loadTestCases(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "load testcases:", err)
		os.Exit(1)
	}

	sentryC := canonicalize(evt.Request.Method, evt.Request.URL, evt.Request.Headers)

	fmt.Println("=== Sentry event ===")
	fmt.Printf("  event_id: %s  status: %d\n", evt.EventID, evt.Contexts.Response.StatusCode)
	fmt.Printf("  %s %s\n", sentryC.method, sentryC.path)
	fmt.Printf("  query keys:  %v\n", sentryC.queryKeys)
	fmt.Printf("  header keys: %v  (flaky filtered)\n", sentryC.headerKeys)
	fmt.Printf("  content-type: %s\n", sentryC.contentType)
	fmt.Printf("  tier1 sig:   %s\n\n", sentryC.tier1())

	type result struct {
		name  string
		tier  int
		score string
	}
	var results []result

	for _, tc := range cases {
		tcC := canonicalizeFromTestCase(tc)
		var tier int
		var score string
		switch {
		case tcC.tier1() == sentryC.tier1():
			tier, score = 1, "exact signature"
		case tcC.tier2() == sentryC.tier2():
			tier, score = 2, "method + path + query keys"
		case tcC.tier3() == sentryC.tier3():
			tier, score = 3, "method + path"
		default:
			tier, score = 0, "no match"
		}
		results = append(results, result{tc.Name, tier, score})

		fmt.Printf("--- testcase: %s ---\n", tc.Name)
		fmt.Printf("  %s %s\n", tcC.method, tcC.path)
		fmt.Printf("  query keys:  %v\n", tcC.queryKeys)
		fmt.Printf("  header keys: %v\n", tcC.headerKeys)
		fmt.Printf("  content-type: %s\n", tcC.contentType)
		fmt.Printf("  tier1 sig:   %s\n", tcC.tier1())
		fmt.Printf("  RESULT: tier %d (%s)\n\n", tier, score)
	}

	fmt.Println("=== Summary (best match wins) ===")
	sort.Slice(results, func(i, j int) bool {
		if results[i].tier == 0 {
			return false
		}
		if results[j].tier == 0 {
			return true
		}
		return results[i].tier < results[j].tier
	})
	for _, r := range results {
		marker := "  "
		if r.tier > 0 && r.tier == results[0].tier {
			marker = "->"
		}
		fmt.Printf("%s %-30s tier=%d  %s\n", marker, r.name, r.tier, r.score)
	}
	if len(results) > 0 && results[0].tier > 0 {
		fmt.Printf("\nReproduce locally:\n  $ keploy test --testcase %s\n", results[0].name)
	} else {
		fmt.Println("\nNo match. Suggested next step: keploy record + replay this request.")
	}
}
