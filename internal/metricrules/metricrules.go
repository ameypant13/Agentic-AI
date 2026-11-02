// Package metricrules is the metrics parser: a library of PromQL rules, each
// returning only the series that are a problem. They run through Grafana
// (internal/grafana), one query per rule per Grafana instance.
package metricrules

import (
	_ "embed"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"sigs.k8s.io/yaml"
)

//go:embed rules.yaml
var defaultRules []byte

type Rule struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Severity string `json:"severity"` // error | warning | info
	Entity   string `json:"entity"`   // container | pod | service | deployment | pvc | node | cluster
	Value    string `json:"value"`    // percent | count | seconds | rate | none
	Expr     string `json:"expr"`
	Meaning  string `json:"meaning"`
	Fix      string `json:"fix"`
}

type File struct {
	Version int    `json:"version"`
	Rules   []Rule `json:"rules"`
}

func Defaults() ([]Rule, error) {
	var f File
	if err := yaml.Unmarshal(defaultRules, &f); err != nil {
		return nil, fmt.Errorf("parse embedded metric rules: %w", err)
	}
	for i := range f.Rules {
		f.Rules[i].Expr = strings.TrimSpace(f.Rules[i].Expr)
	}
	return f.Rules, nil
}

var idRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

var entities = map[string]bool{"container": true, "pod": true, "service": true, "deployment": true, "pvc": true, "node": true, "cluster": true}
var values = map[string]bool{"percent": true, "count": true, "seconds": true, "rate": true, "none": true}

// Validate checks a rule's fields. PromQL itself is checked by running it.
func Validate(r Rule) error {
	switch {
	case !idRe.MatchString(r.ID):
		return fmt.Errorf("id %q must be kebab-case", r.ID)
	case r.Title == "" || r.Expr == "" || r.Meaning == "" || r.Fix == "":
		return fmt.Errorf("%s: title, expr, meaning and fix are required", r.ID)
	case r.Severity != "error" && r.Severity != "warning" && r.Severity != "info":
		return fmt.Errorf("%s: severity must be error, warning or info", r.ID)
	case !entities[r.Entity]:
		return fmt.Errorf("%s: unknown entity %q", r.ID, r.Entity)
	case !values[r.Value]:
		return fmt.Errorf("%s: unknown value kind %q", r.ID, r.Value)
	case !strings.Contains(r.Expr, "cluster"):
		return fmt.Errorf("%s: expr must keep the cluster label", r.ID)
	}
	return nil
}

// EntityKey identifies what an issue is about, from a result series' labels.
func EntityKey(entity string, l map[string]string) (key, name string) {
	switch entity {
	case "container":
		return l["namespace"] + "/" + l["pod"] + "/" + l["container"], l["pod"] + " · " + l["container"]
	case "pod":
		return l["namespace"] + "/" + l["pod"], l["pod"]
	case "service":
		return l["namespace"] + "/" + l["job"], l["job"]
	case "deployment":
		return l["namespace"] + "/" + l["deployment"], l["deployment"]
	case "pvc":
		return l["namespace"] + "/" + l["persistentvolumeclaim"], l["persistentvolumeclaim"]
	case "node":
		n := l["node"]
		if c := l["condition"]; c != "" {
			return n + "/" + c, n + " · " + c
		}
		return n, n
	}
	return "cluster", l["cluster"]
}

// FormatValue renders a series value for the UI.
func FormatValue(kind string, v float64) string {
	switch kind {
	case "percent":
		return fmt.Sprintf("%.0f%%", v*100)
	case "count":
		return fmt.Sprintf("%.0f", v)
	case "seconds":
		return fmt.Sprintf("%.1f s", v)
	case "rate":
		return fmt.Sprintf("%.1f/s", v)
	}
	return ""
}

func SortRules(rs []Rule) {
	sort.Slice(rs, func(i, j int) bool { return rs[i].ID < rs[j].ID })
}
