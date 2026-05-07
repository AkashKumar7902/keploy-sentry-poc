// Package sentry parses the subset of a Sentry event that this POC needs
// to reconstruct an HTTP request and tag a Keploy testcase with provenance.
//
// Real Sentry event payloads are large; we only model what the POC consumes.
package sentry

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strings"
)

type Event struct {
	EventID  string    `json:"event_id"`
	Project  string    `json:"project"`
	Release  string    `json:"release"`
	Level    string    `json:"level"`
	Exception Exception `json:"exception"`
	Request  Request   `json:"request"`
	Contexts Contexts  `json:"contexts"`
}

type Exception struct {
	Values []ExceptionValue `json:"values"`
}

type ExceptionValue struct {
	Type       string     `json:"type"`
	Value      string     `json:"value"`
	Stacktrace Stacktrace `json:"stacktrace"`
}

type Stacktrace struct {
	Frames []Frame `json:"frames"`
}

type Frame struct {
	Filename string `json:"filename"`
	Function string `json:"function"`
	Lineno   int    `json:"lineno"`
}

type Request struct {
	Method  string            `json:"method"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
	Data    json.RawMessage   `json:"data"`
}

type Contexts struct {
	Response Response `json:"response"`
}

type Response struct {
	StatusCode int `json:"status_code"`
}

func Parse(r io.Reader) (*Event, error) {
	var e Event
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&e); err != nil {
		// Sentry payloads have many fields we don't model. Fall back to
		// the lenient decoder when strict mode rejects the unknowns.
		return parseLenient(r)
	}
	return &e, nil
}

func parseLenient(r io.Reader) (*Event, error) {
	body, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	var e Event
	if err := json.Unmarshal(body, &e); err != nil {
		return nil, fmt.Errorf("sentry parse: %w", err)
	}
	return &e, nil
}

// ParseBytes is a convenience wrapper for byte-slice input.
func ParseBytes(b []byte) (*Event, error) {
	return parseLenient(strings.NewReader(string(b)))
}

// SplitURL returns (path, query-string) given the URL field of a Sentry
// request, which is typically absolute (https://host/path?q=v).
func (r Request) SplitURL() (path string, query string, host string, scheme string, err error) {
	u, err := url.Parse(r.URL)
	if err != nil {
		return "", "", "", "", err
	}
	return u.Path, u.RawQuery, u.Host, u.Scheme, nil
}

// BodyString returns the request body as a string, or "" if not set.
// Sentry sends `data` as either a string, an object, or null.
func (r Request) BodyString() string {
	if len(r.Data) == 0 || string(r.Data) == "null" {
		return ""
	}
	// If it's a quoted JSON string, unwrap it. Otherwise return verbatim.
	var s string
	if err := json.Unmarshal(r.Data, &s); err == nil {
		return s
	}
	return string(r.Data)
}

// Provenance is a one-line summary of where this event came from, suitable
// for stuffing into a testcase description field.
func (e *Event) Provenance() string {
	parts := []string{fmt.Sprintf("sentry:%s", e.EventID)}
	if e.Project != "" {
		parts = append(parts, "project="+e.Project)
	}
	if e.Release != "" {
		parts = append(parts, "release="+e.Release)
	}
	if len(e.Exception.Values) > 0 {
		ev := e.Exception.Values[0]
		parts = append(parts, fmt.Sprintf("exc=%s: %s", ev.Type, truncate(ev.Value, 80)))
		if len(ev.Stacktrace.Frames) > 0 {
			top := ev.Stacktrace.Frames[len(ev.Stacktrace.Frames)-1]
			parts = append(parts, fmt.Sprintf("at=%s:%d", top.Filename, top.Lineno))
		}
	}
	if e.Contexts.Response.StatusCode != 0 {
		parts = append(parts, fmt.Sprintf("status=%d", e.Contexts.Response.StatusCode))
	}
	return strings.Join(parts, " | ")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
