package server

import (
	"context"
	"strings"
	"testing"

	"k8s.io/client-go/kubernetes/fake"
	metricsfake "k8s.io/metrics/pkg/client/clientset/versioned/fake"

	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/controlhub"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/grafana"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/kube"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/testutil"
)

func TestKubeAccess(t *testing.T) {
	ch := testutil.NewFakeControlHub()
	t.Cleanup(ch.Close)
	ch.Infrastructures = []map[string]any{{"ID": 0, "id": "infra-1", "clusterName": "acme-eks ", "provider": "aws", "region": "us-east-1"}}
	reg := kube.NewRegistry(kube.Credentials{})
	reg.InjectForTest("infra-1", fake.NewSimpleClientset(), metricsfake.NewSimpleClientset())
	access := KubeAccess(controlhub.New(ch.URL), reg, grafana.NewHub())
	ctx := context.Background()

	for _, name := range []string{"acme-eks", "ACME-EKS", "infra-1"} {
		c, err := access(ctx, testutil.UserToken, name)
		if err != nil || c.Infra.ID != "infra-1" {
			t.Errorf("%s: cluster %v, err %v", name, c, err)
		}
	}
	if _, err := access(ctx, "not-a-token", "acme-eks"); err == nil {
		t.Error("a token control-hub rejects opened a cluster")
	}
	if _, err := access(ctx, testutil.UserToken, "sapep.ep-1"); err == nil || !strings.Contains(err.Error(), "Grafana") {
		t.Errorf("metrics-only cluster: %v", err)
	}
	if _, err := access(ctx, testutil.UserToken, "someone-elses-cluster"); err == nil {
		t.Error("unknown cluster opened")
	}
}
