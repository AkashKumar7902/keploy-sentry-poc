// Package keploycase mirrors the on-disk YAML shape that Keploy uses for
// HTTP testcases (see keploy/keploy pkg/models/testcase.go and http.go).
// We only emit the fields needed for a usable testcase; missing fields
// have sensible zero defaults.
package keploycase

import (
	"fmt"
	"io"
	"time"

	"gopkg.in/yaml.v3"
)

const Version = "api.keploy.io/v1beta1"

type TestCase struct {
	Version     string   `yaml:"version"`
	Kind        string   `yaml:"kind"`
	Name        string   `yaml:"name"`
	Description string   `yaml:"description,omitempty"`
	Created     int64    `yaml:"created"`
	HTTPReq     HTTPReq  `yaml:"http_req"`
	HTTPResp    HTTPResp `yaml:"http_resp"`
	Curl        string   `yaml:"curl,omitempty"`
}

type HTTPReq struct {
	Method     string            `yaml:"method"`
	ProtoMajor int               `yaml:"proto_major"`
	ProtoMinor int               `yaml:"proto_minor"`
	URL        string            `yaml:"url"`
	URLParams  map[string]string `yaml:"url_params,omitempty"`
	Header     map[string]string `yaml:"header"`
	Body       string            `yaml:"body"`
	Timestamp  time.Time         `yaml:"timestamp"`
}

type HTTPResp struct {
	StatusCode    int               `yaml:"status_code"`
	Header        map[string]string `yaml:"header"`
	Body          string            `yaml:"body"`
	StatusMessage string            `yaml:"status_message"`
	ProtoMajor    int               `yaml:"proto_major"`
	ProtoMinor    int               `yaml:"proto_minor"`
}

// New builds a TestCase from a request/response pair.
func New(name, description string, req HTTPReq, resp HTTPResp) *TestCase {
	return &TestCase{
		Version:     Version,
		Kind:        "Http",
		Name:        name,
		Description: description,
		Created:     time.Now().Unix(),
		HTTPReq:     req,
		HTTPResp:    resp,
		Curl:        BuildCurl(req),
	}
}

func (tc *TestCase) WriteYAML(w io.Writer) error {
	enc := yaml.NewEncoder(w)
	enc.SetIndent(2)
	defer enc.Close()
	return enc.Encode(tc)
}

// BuildCurl mirrors pkg.MakeCurlCommand from the keploy repo
// (pkg/util.go:2412), simplified.
func BuildCurl(req HTTPReq) string {
	out := fmt.Sprintf("curl --request %s \\\n  --url %s", req.Method, req.URL)
	for k, v := range req.Header {
		if k == "Content-Length" {
			continue
		}
		out += fmt.Sprintf(" \\\n  --header '%s: %s'", k, v)
	}
	if req.Body != "" {
		out += fmt.Sprintf(" \\\n  --data %q", req.Body)
	}
	return out
}
