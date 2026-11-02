package grafana

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"time"
)

// Read-only discovery calls for the Copilot: what metrics and labels exist,
// which alert rules fire, and what queries the team's dashboards use.

// SeriesLabelValues lists a label's values on series matching selector seen
// in the last window (label "__name__" lists metric names).
func (c *Client) SeriesLabelValues(ctx context.Context, label, selector string, window time.Duration) ([]string, error) {
	prom, _, err := c.Datasources(ctx)
	if err != nil {
		return nil, err
	}
	var r struct {
		Status string   `json:"status"`
		Data   []string `json:"data"`
		Error  string   `json:"error"`
	}
	if err := c.get(ctx, "/api/datasources/proxy/uid/"+prom+"/api/v1/label/"+url.PathEscape(label)+"/values", window1(selector, window), &r); err != nil {
		return nil, err
	}
	if r.Status != "" && r.Status != "success" {
		return nil, fmt.Errorf("label values: %s", r.Error)
	}
	return r.Data, nil
}

// SeriesLabelNames lists the label names on series matching selector.
func (c *Client) SeriesLabelNames(ctx context.Context, selector string, window time.Duration) ([]string, error) {
	prom, _, err := c.Datasources(ctx)
	if err != nil {
		return nil, err
	}
	var r struct {
		Status string   `json:"status"`
		Data   []string `json:"data"`
		Error  string   `json:"error"`
	}
	if err := c.get(ctx, "/api/datasources/proxy/uid/"+prom+"/api/v1/labels", window1(selector, window), &r); err != nil {
		return nil, err
	}
	if r.Status != "" && r.Status != "success" {
		return nil, fmt.Errorf("label names: %s", r.Error)
	}
	return r.Data, nil
}

func window1(selector string, window time.Duration) url.Values {
	end := time.Now()
	return url.Values{"match[]": {selector}, "start": {strconv.FormatInt(end.Add(-window).Unix(), 10)}, "end": {strconv.FormatInt(end.Unix(), 10)}}
}

// AlertRule is one Grafana-managed alert rule and its current instances.
type AlertRule struct {
	Group       string            `json:"group"`
	Folder      string            `json:"folder"`
	Name        string            `json:"name"`
	State       string            `json:"state"` // inactive, pending, firing
	Health      string            `json:"health"`
	Labels      map[string]string `json:"labels"`
	Annotations map[string]string `json:"annotations"`
	Alerts      []RuleAlert       `json:"alerts"`
}

type RuleAlert struct {
	Labels   map[string]string `json:"labels"`
	State    string            `json:"state"`
	ActiveAt string            `json:"activeAt"`
	Value    string            `json:"value"`
}

// GrafanaAlertRules reads Grafana-managed alert rules with their state.
func (c *Client) GrafanaAlertRules(ctx context.Context) ([]AlertRule, error) {
	var r struct {
		Data struct {
			Groups []struct {
				Name  string `json:"name"`
				File  string `json:"file"`
				Rules []struct {
					AlertRule
					Type string `json:"type"`
				} `json:"rules"`
			} `json:"groups"`
		} `json:"data"`
	}
	if err := c.get(ctx, "/api/prometheus/grafana/api/v1/rules", nil, &r); err != nil {
		return nil, err
	}
	var out []AlertRule
	for _, g := range r.Data.Groups {
		for _, rule := range g.Rules {
			if rule.Type != "" && rule.Type != "alerting" {
				continue
			}
			a := rule.AlertRule
			a.Group, a.Folder = g.Name, g.File
			out = append(out, a)
		}
	}
	return out, nil
}

// DashboardHit is one dashboard search result.
type DashboardHit struct {
	UID         string   `json:"uid"`
	Title       string   `json:"title"`
	FolderTitle string   `json:"folderTitle"`
	Tags        []string `json:"tags"`
}

// SearchDashboards finds dashboards by title.
func (c *Client) SearchDashboards(ctx context.Context, query string, limit int) ([]DashboardHit, error) {
	var out []DashboardHit
	q := url.Values{"type": {"dash-db"}, "limit": {strconv.Itoa(limit)}}
	if query != "" {
		q.Set("query", query)
	}
	if err := c.get(ctx, "/api/search", q, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Dashboard is the part of a dashboard model that says what it queries.
type Dashboard struct {
	UID       string     `json:"uid"`
	Title     string     `json:"title"`
	Variables []Variable `json:"variables"`
	Panels    []Panel    `json:"panels"`
}

type Variable struct {
	Name  string `json:"name"`
	Type  string `json:"type"`
	Query string `json:"query,omitempty"`
}

type Panel struct {
	Title   string   `json:"title"`
	Type    string   `json:"type"`
	Row     string   `json:"row,omitempty"`
	Unit    string   `json:"unit,omitempty"`
	Queries []string `json:"queries"`
}

// Dashboard reads one dashboard's variables and panel queries.
func (c *Client) Dashboard(ctx context.Context, uid string) (*Dashboard, error) {
	var r struct {
		Dashboard struct {
			UID    string     `json:"uid"`
			Title  string     `json:"title"`
			Panels []rawPanel `json:"panels"`
			Rows   []struct { // schema before Grafana 5
				Title  string     `json:"title"`
				Panels []rawPanel `json:"panels"`
			} `json:"rows"`
			Templating struct {
				List []struct {
					Name  string          `json:"name"`
					Type  string          `json:"type"`
					Query json.RawMessage `json:"query"`
				} `json:"list"`
			} `json:"templating"`
		} `json:"dashboard"`
	}
	if err := c.get(ctx, "/api/dashboards/uid/"+url.PathEscape(uid), nil, &r); err != nil {
		return nil, err
	}
	d := &Dashboard{UID: r.Dashboard.UID, Title: r.Dashboard.Title, Variables: []Variable{}, Panels: []Panel{}}
	for _, v := range r.Dashboard.Templating.List {
		d.Variables = append(d.Variables, Variable{Name: v.Name, Type: v.Type, Query: queryText(v.Query)})
	}
	var walk func(ps []rawPanel, row string)
	walk = func(ps []rawPanel, row string) {
		for _, p := range ps {
			if p.Type == "row" {
				walk(p.Panels, p.Title)
				continue
			}
			out := Panel{Title: p.Title, Type: p.Type, Row: row, Unit: p.FieldConfig.Defaults.Unit, Queries: []string{}}
			for _, t := range p.Targets {
				if q := firstNonEmpty(t.Expr, t.Query, t.RawSQL); q != "" {
					out.Queries = append(out.Queries, q)
				}
			}
			d.Panels = append(d.Panels, out)
		}
	}
	walk(r.Dashboard.Panels, "")
	for _, row := range r.Dashboard.Rows {
		walk(row.Panels, row.Title)
	}
	return d, nil
}

type rawPanel struct {
	Title       string `json:"title"`
	Type        string `json:"type"`
	FieldConfig struct {
		Defaults struct {
			Unit string `json:"unit"`
		} `json:"defaults"`
	} `json:"fieldConfig"`
	Targets []struct {
		Expr   string `json:"expr"`
		Query  string `json:"query"`
		RawSQL string `json:"rawSql"`
	} `json:"targets"`
	Panels []rawPanel `json:"panels"` // collapsed rows nest their panels
}

// queryText reads a template variable's query, which is a string or, in newer
// dashboards, an object with a "query" field.
func queryText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var o struct {
		Query string `json:"query"`
	}
	if json.Unmarshal(raw, &o) == nil {
		return o.Query
	}
	return ""
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}
