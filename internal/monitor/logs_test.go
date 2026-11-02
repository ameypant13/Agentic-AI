package monitor

import (
	"testing"

	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/kube"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/parser"
)

func TestWorkloadOf(t *testing.T) {
	for pod, want := range map[string]string{
		"metagraph-prophecy-74dbc6c4f7-xxzg9": "metagraph-prophecy",
		"copilot-prophecy-74dbf97b65-wqht2":   "copilot-prophecy",
		"postgres-prophecy-0":                 "postgres-prophecy",
		"sql-sql-km2zf":                       "sql-sql",
	} {
		if got := workloadOf(pod); got != want {
			t.Errorf("workloadOf(%q) = %q, want %q", pod, got, want)
		}
	}
}

func TestLogObservationsGroupByWorkload(t *testing.T) {
	in := kube.Infra{ID: "c1", ClusterName: "prophecy-42d07422", Provider: "gcp"}
	oom := parser.PodHit{RuleID: "jvm-oom", Title: "JVM out of memory", Severity: "error", Count: 6, Active: true}
	scan := &parser.NamespaceScan{Window: "30m0s", Pods: []parser.PodScan{
		{Pod: "metagraph-prophecy-74dbc6c4f7-aaaaa", Errors: 6, Hits: []parser.PodHit{oom}},
		{Pod: "metagraph-prophecy-74dbc6c4f7-bbbbb", Errors: 6, Hits: []parser.PodHit{oom}},
		{Pod: "athena-prophecy-6c9d7b5f4-ccccc", Warnings: 3},
	}}
	obs := logObservations(in, scan)
	if len(obs) != 1 {
		t.Fatalf("observations = %+v", obs)
	}
	o := obs[0]
	if o.key != "log_errors:c1/metagraph-prophecy" || o.severity != "critical" || o.title != "JVM out of memory in metagraph-prophecy" {
		t.Errorf("observation = %+v", o)
	}
	if src := findingSource(o.kind, o.realm, o.entityType, o.entityID); src != "logs:c1" {
		t.Errorf("source = %q", src)
	}
}
