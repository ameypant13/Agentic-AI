package tools

import (
	"context"
	"testing"

	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/grafana"
)

// The Copilot gets Registry; only the MCP server adds CertRepairTool.
func TestRegistryIsReadOnly(t *testing.T) {
	env := &Env{KG: &KnowledgeGraph{}, Logs: &LogReader{}, Grafana: grafana.NewHub(),
		Issues: func(context.Context, string) (string, error) { return "", nil },
		Certs:  func(context.Context, string, bool) (string, error) { return "", nil },
		Costs:  func(context.Context, string, string, string, int) (string, error) { return "", nil }}
	for _, tl := range Registry(env) {
		if tl.Writes || tl.Name == "repair_certificate" || tl.Name == "renew_wildcard_certificate" {
			t.Errorf("Registry offers write tool %s", tl.Name)
		}
	}
	if !CertRepairTool().Writes || !WildcardRenewTool().Writes {
		t.Error("certificate write tools are not marked as writes")
	}
}
