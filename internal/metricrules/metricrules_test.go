package metricrules

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/grafana"
)

func TestDefaultRulesAreValid(t *testing.T) {
	rules, err := Defaults()
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, r := range rules {
		if seen[r.ID] {
			t.Errorf("duplicate id %s", r.ID)
		}
		seen[r.ID] = true
		if err := Validate(r); err != nil {
			t.Error(err)
		}
	}
}

// TestRulesRunLive runs every rule against a real Grafana when
// METRICRULES_GRAFANA_URL and METRICRULES_GRAFANA_TOKEN are set.
func TestRulesRunLive(t *testing.T) {
	url, token := os.Getenv("METRICRULES_GRAFANA_URL"), os.Getenv("METRICRULES_GRAFANA_TOKEN")
	if url == "" || token == "" {
		t.Skip("no live Grafana configured")
	}
	c := grafana.New(grafana.Instance{Name: "live", URL: url, Token: token})
	rules, _ := Defaults()
	for _, r := range rules {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		start := time.Now()
		res, err := c.Query(ctx, r.Expr)
		cancel()
		if err != nil {
			t.Errorf("%s: %v", r.ID, err)
			continue
		}
		missing := 0
		for _, s := range res {
			if s.Labels["cluster"] == "" {
				missing++
			}
		}
		if missing > 0 {
			t.Errorf("%s: %d series without a cluster label", r.ID, missing)
		}
		t.Logf("%-32s %3d issues  %5dms", r.ID, len(res), time.Since(start).Milliseconds())
		for i, s := range res {
			if i == 2 {
				break
			}
			_, name := EntityKey(r.Entity, s.Labels)
			t.Logf("    %s · %s = %s", s.Labels["cluster"], name, FormatValue(r.Value, s.Value))
		}
	}
}
