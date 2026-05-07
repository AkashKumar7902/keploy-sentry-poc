// agent-poll is a stub for the cloud-to-local agent bounce described in
// the brainstorm plan (phase 4). Instead of long-polling api.keploy.io,
// it polls a local JSON file for pending jobs. Once a real cloud queue
// exists, only the source changes — the execution path is identical.
//
// Each job is a Sentry event + target app URL + auth config. The poller
// invokes sentry-replay logic to fire the request and write a testcase.
//
// Usage:
//   agent-poll --jobs jobs.json --out-dir ./testcases [--once]
//
// jobs.json shape:
//   [
//     {
//       "id":        "job-1",
//       "status":    "pending",
//       "event":     { ... full Sentry event ... },
//       "base_url":  "http://localhost:8080",
//       "bearer":    "ey...",
//       "app_id":    "checkout-svc"
//     }
//   ]
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/AkashKumar7902/keploy-sentry-poc/internal/keploycase"
	"github.com/AkashKumar7902/keploy-sentry-poc/internal/sentry"
)

type Job struct {
	ID         string         `json:"id"`
	Status     string         `json:"status"` // pending | done | failed
	Event      *sentry.Event  `json:"event"`
	BaseURL    string         `json:"base_url"`
	Bearer     string         `json:"bearer,omitempty"`
	AppID      string         `json:"app_id,omitempty"`
	Result     *JobResult     `json:"result,omitempty"`
	UpdatedAt  time.Time      `json:"updated_at,omitempty"`
}

type JobResult struct {
	TestCase    string `json:"testcase"`
	StatusCode  int    `json:"status_code"`
	Error       string `json:"error,omitempty"`
}

func main() {
	var (
		jobsPath = flag.String("jobs", "jobs.json", "path to jobs JSON file")
		outDir   = flag.String("out-dir", "testcases", "directory for captured testcases")
		interval = flag.Duration("interval", 5*time.Second, "poll interval (ignored if --once)")
		once     = flag.Bool("once", false, "run one pass and exit")
	)
	flag.Parse()

	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		die("mkdir out-dir: %v", err)
	}

	for {
		processed, err := tick(*jobsPath, *outDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "tick error: %v\n", err)
		} else if processed > 0 {
			fmt.Printf("processed %d job(s)\n", processed)
		}
		if *once {
			return
		}
		time.Sleep(*interval)
	}
}

func tick(jobsPath, outDir string) (int, error) {
	jobs, err := loadJobs(jobsPath)
	if err != nil {
		return 0, err
	}
	processed := 0
	for i := range jobs {
		if jobs[i].Status != "pending" {
			continue
		}
		fmt.Printf("→ running job %s for %s\n", jobs[i].ID, jobs[i].AppID)
		res := executeJob(&jobs[i], outDir)
		jobs[i].Status = "done"
		if res.Error != "" {
			jobs[i].Status = "failed"
		}
		jobs[i].Result = res
		jobs[i].UpdatedAt = time.Now().UTC()
		processed++
	}
	if processed > 0 {
		if err := saveJobs(jobsPath, jobs); err != nil {
			return processed, err
		}
	}
	return processed, nil
}

func executeJob(j *Job, outDir string) *JobResult {
	if j.Event == nil {
		return &JobResult{Error: "missing event"}
	}
	req, err := buildHTTPRequest(j)
	if err != nil {
		return &JobResult{Error: fmt.Sprintf("build: %v", err)}
	}
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return &JobResult{Error: fmt.Sprintf("fire: %v", err)}
	}
	defer resp.Body.Close()
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, resp.Body); err != nil {
		return &JobResult{Error: fmt.Sprintf("read response: %v", err)}
	}

	tc := keploycase.New(
		fmt.Sprintf("sentry-%s", j.ID),
		fmt.Sprintf("Captured via agent-poll job %s | %s", j.ID, j.Event.Provenance()),
		keploycase.HTTPReq{
			Method:     req.Method,
			ProtoMajor: 1, ProtoMinor: 1,
			URL:       req.URL.RequestURI(),
			URLParams: flattenQuery(req.URL.Query()),
			Header:    flattenHeader(req.Header),
			Body:      j.Event.Request.BodyString(),
			Timestamp: time.Now().UTC(),
		},
		keploycase.HTTPResp{
			StatusCode:    resp.StatusCode,
			Header:        flattenHeader(resp.Header),
			Body:          buf.String(),
			StatusMessage: http.StatusText(resp.StatusCode),
			ProtoMajor:    1, ProtoMinor: 1,
		},
	)

	out := filepath.Join(outDir, fmt.Sprintf("%s.yaml", tc.Name))
	f, err := os.Create(out)
	if err != nil {
		return &JobResult{Error: fmt.Sprintf("create yaml: %v", err)}
	}
	defer f.Close()
	if err := tc.WriteYAML(f); err != nil {
		return &JobResult{Error: fmt.Sprintf("write yaml: %v", err)}
	}
	return &JobResult{TestCase: out, StatusCode: resp.StatusCode}
}

func buildHTTPRequest(j *Job) (*http.Request, error) {
	url := j.Event.Request.URL
	if j.BaseURL != "" {
		path, q, _, _, err := j.Event.Request.SplitURL()
		if err != nil {
			return nil, err
		}
		url = strings.TrimRight(j.BaseURL, "/") + path
		if q != "" {
			url += "?" + q
		}
	}
	body := j.Event.Request.BodyString()
	req, err := http.NewRequestWithContext(context.Background(), j.Event.Request.Method, url, strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	for k, v := range j.Event.Request.Headers {
		lk := strings.ToLower(k)
		if lk == "host" || lk == "content-length" || strings.HasPrefix(lk, "x-amz") || lk == "authorization" || lk == "traceparent" {
			continue
		}
		req.Header.Set(k, v)
	}
	if j.Bearer != "" {
		req.Header.Set("Authorization", "Bearer "+j.Bearer)
	}
	return req, nil
}

func loadJobs(path string) ([]Job, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var jobs []Job
	if err := json.Unmarshal(b, &jobs); err != nil {
		return nil, err
	}
	return jobs, nil
}

func saveJobs(path string, jobs []Job) error {
	b, err := json.MarshalIndent(jobs, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
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

func die(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "agent-poll: "+format+"\n", args...)
	os.Exit(1)
}
