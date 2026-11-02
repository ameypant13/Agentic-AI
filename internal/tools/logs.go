package tools

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/redact"
)

const saDir = "/var/run/secrets/kubernetes.io/serviceaccount"

// LogReader reads control-hub's own pod logs through the Kubernetes API, using
// the pod's service account. It needs get/list on pods and get on pods/log in
// the namespace (see deploy/k8s).
type LogReader struct {
	apiURL    string
	namespace string
	selector  string
	http      *http.Client
}

// NewInClusterLogReader returns nil outside a cluster or when namespace is empty.
func NewInClusterLogReader(namespace, selector string) *LogReader {
	host, port := os.Getenv("KUBERNETES_SERVICE_HOST"), os.Getenv("KUBERNETES_SERVICE_PORT")
	if namespace == "" || host == "" {
		return nil
	}
	ca, err := os.ReadFile(saDir + "/ca.crt")
	if err != nil {
		return nil
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(ca)
	return &LogReader{
		apiURL:    "https://" + host + ":" + port,
		namespace: namespace,
		selector:  selector,
		http:      &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}}},
	}
}

func (l *LogReader) get(ctx context.Context, path string, q url.Values) (io.ReadCloser, error) {
	// The token file is rotated by the kubelet, so read it on every call.
	token, err := os.ReadFile(saDir + "/token")
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, l.apiURL+path+"?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(token)))
	resp, err := l.http.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1000))
		resp.Body.Close()
		return nil, fmt.Errorf("kubernetes API %s: %d %s", path, resp.StatusCode, b)
	}
	return resp.Body, nil
}

// Search returns up to maxLines log lines from control-hub's pods in the last
// window that contain every term in contains (case-insensitive).
func (l *LogReader) Search(ctx context.Context, contains []string, window time.Duration, maxLines int) ([]string, error) {
	body, err := l.get(ctx, "/api/v1/namespaces/"+l.namespace+"/pods", url.Values{"labelSelector": {l.selector}})
	if err != nil {
		return nil, err
	}
	var pods struct {
		Items []struct {
			Metadata struct{ Name string } `json:"metadata"`
		} `json:"items"`
	}
	err = json.NewDecoder(body).Decode(&pods)
	body.Close()
	if err != nil {
		return nil, err
	}

	terms := make([]string, 0, len(contains))
	for _, c := range contains {
		if c = strings.ToLower(strings.TrimSpace(c)); c != "" {
			terms = append(terms, c)
		}
	}

	var lines []string
	for _, p := range pods.Items {
		q := url.Values{"sinceSeconds": {strconv.Itoa(int(window.Seconds()))}, "limitBytes": {strconv.Itoa(50 << 20)}}
		rc, err := l.get(ctx, "/api/v1/namespaces/"+l.namespace+"/pods/"+p.Metadata.Name+"/log", q)
		if err != nil {
			return nil, err
		}
		sc := bufio.NewScanner(rc)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		for sc.Scan() {
			line := sc.Text()
			if matchesAll(strings.ToLower(line), terms) {
				lines = append(lines, redact.LogLine(line))
				if len(lines) > maxLines {
					lines = lines[1:] // keep the most recent maxLines
				}
			}
		}
		rc.Close()
	}
	return lines, nil
}

func matchesAll(line string, terms []string) bool {
	for _, t := range terms {
		if !strings.Contains(line, t) {
			return false
		}
	}
	return true
}

func logTools() []Tool {
	return []Tool{{
		Name: "search_controlhub_logs",
		Description: "Search control-hub's own logs (the service that provisions clusters). Lines must contain every term. " +
			"Use the tenant name, hostname, control plane ID or job ID as terms to follow a provisioning run step by step; " +
			"errors are logged as [ERROR]. Secrets are masked.",
		Properties: map[string]any{
			"contains":      map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Terms every returned line must contain (case-insensitive)."},
			"since_minutes": integer("How far back to search. Default 180, max 4320 (3 days)."),
			"max_lines":     integer("Most recent matching lines to return. Default 200, max 500."),
		},
		Required: []string{"contains"},
		Label:    "Read the provisioning logs",
		Run: func(ctx context.Context, env *Env, input json.RawMessage) (string, error) {
			in, err := decode[struct {
				Contains     []string `json:"contains"`
				SinceMinutes int      `json:"since_minutes"`
				MaxLines     int      `json:"max_lines"`
			}](input)
			if err != nil {
				return "", err
			}
			if len(in.Contains) == 0 {
				return "", fmt.Errorf("contains needs at least one term")
			}
			since := clamp(in.SinceMinutes, 180, 4320)
			maxLines := clamp(in.MaxLines, 200, 500)
			lines, err := env.Logs.Search(ctx, in.Contains, time.Duration(since)*time.Minute, maxLines)
			if err != nil {
				return "", err
			}
			return marshal(map[string]any{"matchedLines": len(lines), "lines": lines})
		},
	}}
}

func clamp(v, def, max int) int {
	if v <= 0 {
		return def
	}
	if v > max {
		return max
	}
	return v
}
