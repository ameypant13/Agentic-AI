package server

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/grafana"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/metricrules"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/parser"
)

// IssuesFunc builds the Copilot's current_issues lookup over the metrics and
// log parsers' stored results. cluster "" means every known cluster.
func IssuesFunc(hub *grafana.Hub, metrics *metricrules.Worker, logs *parser.Worker) func(ctx context.Context, cluster string) (string, error) {
	return func(_ context.Context, cluster string) (string, error) {
		type item struct {
			Cluster  string `json:"cluster"`
			Source   string `json:"source"`
			Severity string `json:"severity"`
			Title    string `json:"title"`
			Entity   string `json:"entity"`
			Value    string `json:"value,omitempty"`
			Since    string `json:"since"`
			Rule     string `json:"rule"`
		}
		var out []item
		for _, in := range logs.Clusters() {
			if cluster != "" && !strings.EqualFold(in.ClusterName, cluster) {
				continue
			}
			if m := hub.Resolve(in.ClusterName); m != nil {
				for _, is := range metrics.Issues(m.Instance, m.PromCluster) {
					if is.Active {
						out = append(out, item{Cluster: in.ClusterName, Source: "metrics", Severity: is.Severity, Title: is.Title,
							Entity: is.Namespace + "/" + is.EntityName, Value: is.ValueText, Since: is.FirstSeen.UTC().Format("2006-01-02 15:04"), Rule: is.RuleID})
					}
				}
			}
			for _, ns := range logs.Namespaces(in.ID) {
				for _, p := range logs.Summary(in.ID, ns).Pods {
					for _, h := range p.Hits {
						if h.Active && h.Count > 0 {
							out = append(out, item{Cluster: in.ClusterName, Source: "logs", Severity: h.Severity, Title: h.Title,
								Entity: ns + "/" + p.Pod, Value: "×" + itoa(h.Count) + " in 1 h", Since: h.FirstSeen.UTC().Format("2006-01-02 15:04"), Rule: h.RuleID})
						}
					}
				}
			}
		}
		if len(out) > 150 {
			out = out[:150]
		}
		raw, err := json.Marshal(map[string]any{"count": len(out), "issues": out})
		return string(raw), err
	}
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}
