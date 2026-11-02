// Package grafana reads metrics (Prometheus/Thanos) and logs (Loki) through a
// Grafana instance's datasource proxy, with a service-account token. It only
// ever queries; nothing in Grafana is changed.
package grafana

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Instance is one Grafana (e.g. the AWS or Azure monitoring stack).
type Instance struct {
	Name  string
	URL   string
	Token string
}

type Client struct {
	inst Instance
	http *http.Client

	mu      sync.Mutex
	promUID string
	lokiUID string
}

func New(inst Instance) *Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.TLSClientConfig = newAIAVerifier(nil).tlsConfig()
	return &Client{inst: inst, http: &http.Client{Timeout: 90 * time.Second, Transport: tr}}
}

func (c *Client) Name() string { return c.inst.Name }
func (c *Client) URL() string  { return c.inst.URL }

// get retries Grafana's intermittent "user.sync.fetch" 500, which service
// accounts hit on some instances even though the next identical call succeeds.
func (c *Client) get(ctx context.Context, path string, q url.Values, out any) error {
	var err error
	for attempt := 0; attempt < 4; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(attempt) * 400 * time.Millisecond):
			}
		}
		if err = c.getOnce(ctx, path, q, out); err == nil || !strings.Contains(err.Error(), "user.sync.fetch") {
			return err
		}
	}
	return err
}

func (c *Client) getOnce(ctx context.Context, path string, q url.Values, out any) error {
	u := strings.TrimRight(c.inst.URL, "/") + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.inst.Token)
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("grafana %s: %w", c.inst.Name, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("grafana %s %s: %d %s", c.inst.Name, path, resp.StatusCode, truncate(string(raw), 300))
	}
	return json.Unmarshal(raw, out)
}

// Datasources finds the Prometheus (default first) and Loki datasource UIDs.
func (c *Client) Datasources(ctx context.Context) (prom, loki string, err error) {
	c.mu.Lock()
	if c.promUID != "" {
		p, l := c.promUID, c.lokiUID
		c.mu.Unlock()
		return p, l, nil
	}
	c.mu.Unlock()
	var list []struct {
		UID       string `json:"uid"`
		Type      string `json:"type"`
		IsDefault bool   `json:"isDefault"`
	}
	if err := c.get(ctx, "/api/datasources", nil, &list); err != nil {
		return "", "", err
	}
	for _, d := range list {
		switch d.Type {
		case "prometheus":
			if prom == "" || d.IsDefault {
				prom = d.UID
			}
		case "loki":
			if loki == "" {
				loki = d.UID
			}
		}
	}
	if prom == "" {
		return "", "", errors.New("no Prometheus datasource in " + c.inst.Name)
	}
	c.mu.Lock()
	c.promUID, c.lokiUID = prom, loki
	c.mu.Unlock()
	return prom, loki, nil
}

// --- Prometheus ---

type Sample struct {
	Labels map[string]string `json:"labels"`
	Value  float64           `json:"value"`
}

type Series struct {
	Labels map[string]string `json:"labels"`
	Points [][2]float64      `json:"points"` // [unix seconds, value]
}

type promResp struct {
	Status string `json:"status"`
	Error  string `json:"error"`
	Data   struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Metric map[string]string `json:"metric"`
			Value  [2]any            `json:"value"`
			Values [][2]any          `json:"values"`
		} `json:"result"`
	} `json:"data"`
}

// Query runs an instant PromQL query.
func (c *Client) Query(ctx context.Context, expr string) ([]Sample, error) {
	return c.QueryAt(ctx, expr, time.Time{})
}

// QueryAt runs an instant PromQL query evaluated at t (now when zero).
func (c *Client) QueryAt(ctx context.Context, expr string, t time.Time) ([]Sample, error) {
	prom, _, err := c.Datasources(ctx)
	if err != nil {
		return nil, err
	}
	q := url.Values{"query": {expr}}
	if !t.IsZero() {
		q.Set("time", strconv.FormatInt(t.Unix(), 10))
	}
	var r promResp
	if err := c.get(ctx, "/api/datasources/proxy/uid/"+prom+"/api/v1/query", q, &r); err != nil {
		return nil, err
	}
	if r.Status != "success" {
		return nil, fmt.Errorf("promql: %s", r.Error)
	}
	out := make([]Sample, 0, len(r.Data.Result))
	for _, res := range r.Data.Result {
		v, ok := num(res.Value[1])
		if !ok {
			continue
		}
		out = append(out, Sample{Labels: res.Metric, Value: v})
	}
	return out, nil
}

// QueryRange runs a PromQL range query.
func (c *Client) QueryRange(ctx context.Context, expr string, start, end time.Time, step time.Duration) ([]Series, error) {
	prom, _, err := c.Datasources(ctx)
	if err != nil {
		return nil, err
	}
	q := url.Values{"query": {expr}, "start": {strconv.FormatInt(start.Unix(), 10)},
		"end": {strconv.FormatInt(end.Unix(), 10)}, "step": {strconv.Itoa(int(step.Seconds()))}}
	var r promResp
	if err := c.get(ctx, "/api/datasources/proxy/uid/"+prom+"/api/v1/query_range", q, &r); err != nil {
		return nil, err
	}
	if r.Status != "success" {
		return nil, fmt.Errorf("promql: %s", r.Error)
	}
	out := make([]Series, 0, len(r.Data.Result))
	for _, res := range r.Data.Result {
		s := Series{Labels: res.Metric}
		for _, p := range res.Values {
			t, ok1 := num(p[0])
			v, ok2 := num(p[1])
			if ok1 && ok2 {
				s.Points = append(s.Points, [2]float64{t, v})
			}
		}
		out = append(out, s)
	}
	return out, nil
}

// LabelValues lists a label's values (e.g. "cluster").
func (c *Client) LabelValues(ctx context.Context, label string) ([]string, error) {
	prom, _, err := c.Datasources(ctx)
	if err != nil {
		return nil, err
	}
	var r struct {
		Status string   `json:"status"`
		Data   []string `json:"data"`
	}
	if err := c.get(ctx, "/api/datasources/proxy/uid/"+prom+"/api/v1/label/"+url.PathEscape(label)+"/values", nil, &r); err != nil {
		return nil, err
	}
	return r.Data, nil
}

// --- Loki ---

type LogLine struct {
	TS     time.Time         `json:"ts"`
	Line   string            `json:"line"`
	Labels map[string]string `json:"labels"`
}

// Logs runs a LogQL range query, oldest first, at most limit lines.
func (c *Client) Logs(ctx context.Context, logql string, start, end time.Time, limit int) ([]LogLine, error) {
	_, loki, err := c.Datasources(ctx)
	if err != nil {
		return nil, err
	}
	if loki == "" {
		return nil, errors.New("no Loki datasource in " + c.inst.Name)
	}
	q := url.Values{"query": {logql}, "start": {strconv.FormatInt(start.UnixNano(), 10)},
		"end": {strconv.FormatInt(end.UnixNano(), 10)}, "limit": {strconv.Itoa(limit)}, "direction": {"forward"}}
	var r struct {
		Status string `json:"status"`
		Data   struct {
			Result []struct {
				Stream map[string]string `json:"stream"`
				Values [][2]string       `json:"values"`
			} `json:"result"`
		} `json:"data"`
	}
	if err := c.get(ctx, "/api/datasources/proxy/uid/"+loki+"/loki/api/v1/query_range", q, &r); err != nil {
		return nil, err
	}
	var out []LogLine
	for _, s := range r.Data.Result {
		for _, v := range s.Values {
			ns, err := strconv.ParseInt(v[0], 10, 64)
			if err != nil {
				continue
			}
			out = append(out, LogLine{TS: time.Unix(0, ns), Line: v[1], Labels: s.Stream})
		}
	}
	return out, nil
}

// LokiLabelValues lists a Loki label's values across all streams.
func (c *Client) LokiLabelValues(ctx context.Context, label string) ([]string, error) {
	_, loki, err := c.Datasources(ctx)
	if err != nil || loki == "" {
		return nil, err
	}
	var r struct {
		Data []string `json:"data"`
	}
	if err := c.get(ctx, "/api/datasources/proxy/uid/"+loki+"/loki/api/v1/label/"+url.PathEscape(label)+"/values", nil, &r); err != nil {
		return nil, err
	}
	return r.Data, nil
}

func num(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case string:
		f, err := strconv.ParseFloat(x, 64)
		return f, err == nil
	}
	return 0, false
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
