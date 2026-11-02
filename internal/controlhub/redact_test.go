package controlhub

import (
	"strings"
	"testing"
)

func TestRedact(t *testing.T) {
	in := `{"id":"cp-1","systemUserTokenId":7,"users":[{"username":"admin","password":"hunter2"}],
		"config":{"request":{"apiKey":"sk-1","tenantName":"acme"}},"token":"","postDeployResult":{"accessToken":"abc"}}`
	out := string(Redact([]byte(in)))

	for _, leaked := range []string{"hunter2", "sk-1", "abc"} {
		if strings.Contains(out, leaked) {
			t.Errorf("secret %q leaked: %s", leaked, out)
		}
	}
	for _, kept := range []string{`"cp-1"`, `"systemUserTokenId":7`, `"acme"`, `"admin"`, `"token":""`} {
		if !strings.Contains(out, kept) {
			t.Errorf("expected %s to be kept: %s", kept, out)
		}
	}
}

func TestRedactNonJSON(t *testing.T) {
	if got := string(Redact([]byte("plain error"))); got != "plain error" {
		t.Errorf("non-JSON input changed: %q", got)
	}
}
