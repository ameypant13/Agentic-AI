package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/certs"
)

func withCerts(t *testing.T, h *harness, items ...certs.Item) {
	t.Helper()
	dir := t.TempDir()
	raw, _ := json.Marshal(certs.Inventory{Version: certs.ScannerVersion, ScannedAt: time.Now(), Items: items})
	if err := os.WriteFile(filepath.Join(dir, "inventory.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	w, err := certs.NewWorker(certs.Sources{}, dir, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	h.server.certs = w
}

func TestCertificateRepair(t *testing.T) {
	h := newHarness(t)
	withCerts(t, h,
		certs.Item{Key: "acm:arn:x", Kind: certs.KindACM, Host: "*.app.prophecy.ai", Status: certs.StatusCritical,
			Repair: &certs.RepairTarget{Kind: "acm-validation", CertificateARN: "arn:aws:acm:us-east-1:111111111111:certificate/x"}},
		certs.Item{Key: "tenant:ok", Kind: certs.KindTenant, Host: "ok.app.prophecy.ai", Status: certs.StatusOK},
		certs.Item{Key: "secret:w", Kind: certs.KindWildcard, Host: "*.app.prophecy.ai", Status: certs.StatusOK,
			Repair: &certs.RepairTarget{Kind: "wildcard-renew", SecretName: "wildcard-app-prophecy-ai-tls-secret"}})
	admin, sre := h.login("admin@prophecy.io"), h.login("sre@prophecy.io")

	req := map[string]any{"key": "acm:arn:x", "dryRun": true}
	if st := h.call(admin, "POST", "/api/certificates/repair", req, nil); st != 403 {
		t.Fatalf("repair with CERT_REPAIR_ENABLED off: status %d, want 403", st)
	}
	h.server.cfg.CertRepairEnabled = true

	if st := h.call(sre, "POST", "/api/certificates/repair", req, nil); st != 403 {
		t.Fatalf("non-admin repair: status %d, want 403", st)
	}
	if st := h.call(admin, "POST", "/api/certificates/repair", map[string]any{"key": "tenant:ok"}, nil); st != 400 {
		t.Fatalf("repair of a healthy cert: status %d, want 400", st)
	}
	if st := h.call(admin, "POST", "/api/certificates/repair", map[string]any{"key": "nope"}, nil); st != 400 {
		t.Fatalf("repair of an unknown key: status %d, want 400", st)
	}
	if len(h.ch.Posts) != 0 {
		t.Fatalf("control-hub called for refused requests: %v", h.ch.Posts)
	}

	if st := h.call(admin, "POST", "/api/certificates/repair", req, nil); st != 200 {
		t.Fatalf("dry run: status %d", st)
	}
	if len(h.ch.Posts) != 1 {
		t.Fatalf("posts = %v", h.ch.Posts)
	}
	post := h.ch.Posts[0]
	for _, want := range []string{"POST /api/certificates/repair", `"kind":"acm-validation"`,
		`"certificateArn":"arn:aws:acm:us-east-1:111111111111:certificate/x"`, `"dryRun":true`} {
		if !strings.Contains(post, want) {
			t.Errorf("control-hub request %q lacks %s", post, want)
		}
	}
	// A wildcard row goes to control-hub's wildcard renew endpoint with its secret name.
	if st := h.call(admin, "POST", "/api/certificates/repair", map[string]any{"key": "secret:w", "dryRun": true}, nil); st != 200 {
		t.Fatalf("wildcard dry run: status %d", st)
	}
	if len(h.ch.Posts) != 2 || !strings.Contains(h.ch.Posts[1], "POST /api/certificates/wildcards/renew") ||
		!strings.Contains(h.ch.Posts[1], `"secretName":"wildcard-app-prophecy-ai-tls-secret"`) || !strings.Contains(h.ch.Posts[1], `"dryRun":true`) {
		t.Errorf("wildcard request = %v", h.ch.Posts)
	}
	audit, _ := h.st.ListAudit(10)
	if len(audit) < 2 || audit[0].Action != "certificate_repair_plan" || audit[0].Actor != "admin@prophecy.io" {
		t.Errorf("audit = %+v", audit)
	}
}
