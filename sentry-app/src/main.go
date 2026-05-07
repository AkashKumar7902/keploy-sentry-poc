// sentry-app is a stub backend for the public Sentry integration. It
// implements the three endpoints declared in
// sentry-app/manifest/integration.json:
//
//   POST /v1/integrations/sentry/oauth/callback
//   POST /v1/integrations/sentry/match     (issue-link → click)
//   POST /v1/integrations/sentry/record    (create action)
//   POST /v1/integrations/sentry/webhook   (future: event subscriptions)
//
// In production these would run on api.keploy.io and talk to the cloud
// testcase index. Here they fake just enough for an end-to-end demo.
//
// Run:
//   go run ./sentry-app/src --addr :9090
//
// Then point the Sentry integration's webhook/redirect URLs at this.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/AkashKumar7902/keploy-sentry-poc/internal/sentry"
)

func main() {
	addr := flag.String("addr", ":9090", "listen address")
	flag.Parse()

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/integrations/sentry/oauth/callback", oauthCallback)
	mux.HandleFunc("/v1/integrations/sentry/match", handleMatch)
	mux.HandleFunc("/v1/integrations/sentry/record", handleRecord)
	mux.HandleFunc("/v1/integrations/sentry/webhook", handleWebhook)
	mux.HandleFunc("/v1/integrations/sentry/apps", handleAppsLookup)
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "ok")
	})

	log.Printf("sentry-app stub listening on %s", *addr)
	log.Fatal(http.ListenAndServe(*addr, withLogging(mux)))
}

func withLogging(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("→ %s %s", r.Method, r.URL.Path)
		h.ServeHTTP(w, r)
	})
}

// oauthCallback completes the install handshake. In production we'd
// exchange the code for a token and persist installation metadata.
func oauthCallback(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"installation_id": "stub-install-1",
		"status":          "ok",
		"message":         "Keploy installed. Visit app.keploy.io to finish workspace mapping.",
	})
}

// handleMatch is the click target for the "Reproduce in Keploy" button.
// Sentry posts the issue + event payload; we return an action shape that
// Sentry renders inline.
func handleMatch(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	evt, err := tryParseEvent(body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "could not parse event: "+err.Error())
		return
	}

	// V1 stub: pretend we matched a testcase if the path looks familiar.
	// In production this calls the cloud signature index from spike/.
	matched := strings.Contains(strings.ToLower(evt.Request.URL), "/api/checkout")

	if matched {
		writeJSON(w, 200, map[string]any{
			"webUrl":     "https://app.keploy.io/testcase/sentry-" + evt.EventID,
			"identifier": "sentry-" + evt.EventID,
			"project":    "Keploy",
			"action":     "match",
			"command":    fmt.Sprintf("keploy test --testcase sentry-%s", evt.EventID),
			"message":    "Found a matching testcase. Run the command above to reproduce locally.",
			"provenance": evt.Provenance(),
		})
		return
	}

	writeJSON(w, 200, map[string]any{
		"action":     "no-match",
		"curl":       reconstructCurl(evt, ""),
		"message":    "No matching testcase. Click 'Create' to record one, or run the curl above under `keploy record`.",
		"provenance": evt.Provenance(),
	})
}

// handleRecord is the target of the create action — the user supplied
// keploy_app_id and bearer_token via the form. We enqueue a job (here:
// log it) and return the testcase id that will eventually appear.
func handleRecord(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	evt, err := tryParseEvent(body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "could not parse event: "+err.Error())
		return
	}

	formValues := extractFormValues(body)
	log.Printf("record job enqueued: app=%s bearer=%s event=%s",
		formValues["keploy_app_id"], maskToken(formValues["bearer_token"]), evt.EventID)

	writeJSON(w, 200, map[string]any{
		"webUrl":     "https://app.keploy.io/testcase/sentry-" + evt.EventID,
		"identifier": "sentry-" + evt.EventID,
		"project":    formValues["keploy_app_id"],
		"action":     "queued",
		"message":    "Record job enqueued. Your local Keploy agent will pick it up and a testcase will appear shortly.",
		"provenance": evt.Provenance(),
	})
}

// handleWebhook receives event subscriptions (issue.created, etc.).
// V1 just logs them.
func handleWebhook(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	log.Printf("webhook received: %d bytes", len(body))
	w.WriteHeader(http.StatusNoContent)
}

// handleAppsLookup serves the dropdown of Keploy apps the user can pick
// from in the create form. Stubbed.
func handleAppsLookup(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, []map[string]string{
		{"label": "checkout-svc", "value": "checkout-svc"},
		{"label": "users-api", "value": "users-api"},
		{"label": "billing-worker", "value": "billing-worker"},
	})
}

// --- helpers --------------------------------------------------------

func tryParseEvent(body []byte) (*sentry.Event, error) {
	// Sentry posts a wrapped payload in real life: { "data": { ... event ... }, ... }.
	// Try the wrapped form first, then fall through to a bare event.
	var wrapped struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &wrapped); err == nil && len(wrapped.Data) > 0 {
		if evt, err := sentry.ParseBytes(wrapped.Data); err == nil {
			return evt, nil
		}
	}
	return sentry.ParseBytes(body)
}

func extractFormValues(body []byte) map[string]string {
	var wrapped struct {
		Fields map[string]string `json:"fields"`
	}
	if err := json.Unmarshal(body, &wrapped); err == nil && wrapped.Fields != nil {
		return wrapped.Fields
	}
	return map[string]string{}
}

func reconstructCurl(evt *sentry.Event, bearer string) string {
	out := fmt.Sprintf("curl --request %s --url %s", evt.Request.Method, evt.Request.URL)
	for k, v := range evt.Request.Headers {
		lk := strings.ToLower(k)
		if lk == "authorization" || lk == "traceparent" || lk == "x-request-id" {
			continue
		}
		out += fmt.Sprintf(" --header '%s: %s'", k, v)
	}
	if bearer != "" {
		out += fmt.Sprintf(" --header 'Authorization: Bearer %s'", bearer)
	}
	if body := evt.Request.BodyString(); body != "" {
		out += fmt.Sprintf(" --data %q", body)
	}
	return out
}

func maskToken(t string) string {
	if len(t) <= 6 {
		return "***"
	}
	return t[:3] + "***" + t[len(t)-3:]
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	_ = enc.Encode(body)
	_, _ = w.Write(buf.Bytes())
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
