package server

import (
	"errors"
	"strings"
	"testing"
)

func TestClusterErrorMessage(t *testing.T) {
	cases := map[string]string{
		"get AKS credentials: RESPONSE 404: 404 Not Found\nERROR CODE: ResourceGroupNotFound":      "Cluster not found in Azure",
		"get GKE cluster: googleapi: Error 404: Not found: projects/x":                             "Cluster not found in GCP",
		`Get "https://x.hcp.westus2.azmk8s.io:443/api/v1/nodes": dial tcp: lookup x: no such host`: "no longer resolves",
		"some other failure": "some other failure",
	}
	for in, want := range cases {
		provider := "azure"
		if strings.Contains(in, "GKE") {
			provider = "gcp"
		}
		if got := clusterErrorMessage(provider, errors.New(in)); !strings.Contains(got, want) {
			t.Errorf("%q -> %q, want it to contain %q", in, got, want)
		}
	}
}

func TestParseRange(t *testing.T) {
	for in, want := range map[string]string{"7d": "168h0m0s", "6h": "6h0m0s", "90m": "1h30m0s"} {
		if d, err := parseRange(in); err != nil || d.String() != want {
			t.Errorf("parseRange(%q) = %v, %v", in, d, err)
		}
	}
}
