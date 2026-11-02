package kube

import (
	"encoding/json"
	"testing"
)

// The shape control-hub's GET /api/infrastructures returns: gorm.Model's
// numeric "ID" next to the string "id".
func TestInfraDecodesControlHubShape(t *testing.T) {
	raw := `{"ID":0,"CreatedAt":"2025-11-25T14:41:41Z","id":"cce97755","clusterName":"medmutual","provider":"azure",
		"region":"northcentralus","status":"ready","kubernetesVersion":"1.33",
		"infrastructureDetails":{"azureDetails":{"resourceGroupName":"rg-medmutual"},"isByok":false}}`
	var in Infra
	if err := json.Unmarshal([]byte(raw), &in); err != nil {
		t.Fatal(err)
	}
	if in.ID != "cce97755" || in.ClusterName != "medmutual" || in.Details.Azure == nil || in.Details.Azure.ResourceGroupName != "rg-medmutual" {
		t.Errorf("decoded %+v", in)
	}
}
