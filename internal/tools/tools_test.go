package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/redact"
)

func TestRedactLogLine(t *testing.T) {
	line := `[INFO] login Authorization: Bearer abcdefghijklmnop token=eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N password: "s3cret" host=capgroup.app.prophecy.ai`
	got := redact.LogLine(line)
	for _, leaked := range []string{"abcdefghijklmnop", "eyJhbGci", "s3cret"} {
		if strings.Contains(got, leaked) {
			t.Errorf("%q leaked: %s", leaked, got)
		}
	}
	if !strings.Contains(got, "capgroup.app.prophecy.ai") {
		t.Errorf("useful context removed: %s", got)
	}
}

func TestProbePolicy(t *testing.T) {
	p := DefaultProbePolicy()
	for _, ok := range []string{"capgroup.app.prophecy.ai", "x.cloud.prophecy.io."} {
		if err := p.check(ok); err != nil {
			t.Errorf("%s rejected: %v", ok, err)
		}
	}
	for _, bad := range []string{"control-hub", "postgres.control-hub-prod.svc", "prophecy.ai.evil.com", "169.254.169.254"} {
		if err := p.check(bad); err == nil {
			t.Errorf("%s allowed", bad)
		}
	}
}

func TestKnowledgeGraphOnlyServesItsOwnFiles(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{"INDEX.md": "router", "04-controlplane.md": "cp"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	kg := LoadKnowledgeGraph(dir)
	if kg == nil {
		t.Fatal("not loaded")
	}
	if got, err := kg.Read("04-controlplane"); err != nil || got != "cp" {
		t.Errorf("read node: %q %v", got, err)
	}
	if _, err := kg.Read("../../etc/passwd"); err == nil {
		t.Error("path traversal allowed")
	}
	if LoadKnowledgeGraph(t.TempDir()) != nil {
		t.Error("loaded a directory without INDEX.md")
	}
}
