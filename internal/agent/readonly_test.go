package agent

import (
	"testing"

	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/audit"
)

func TestReadOnlyKeepsDryRunActions(t *testing.T) {
	for _, ro := range []bool{false, true} {
		tr := &turn{a: (&Agent{}).WithReadOnly(ro)}
		names := map[string]bool{}
		for _, tl := range tr.consoleTools() {
			names[tl.Name] = true
		}
		// Read-only keeps propose_action: proposals are dry runs and ExecuteAction refuses them.
		if !names["propose_action"] || !names["record_investigation"] {
			t.Errorf("readOnly=%v: tools = %v", ro, names)
		}
	}
}

func TestAuditToolAdminsOnly(t *testing.T) {
	a := (&Agent{}).WithAudit(audit.NewWithLister([]string{"p"}, nil))
	for _, admin := range []bool{false, true} {
		tr := &turn{a: a, user: User{IsAdmin: admin}}
		has := false
		for _, tl := range tr.consoleTools() {
			has = has || tl.Name == "audit_search"
		}
		if has != admin {
			t.Errorf("admin=%v: audit_search offered=%v", admin, has)
		}
	}
}
