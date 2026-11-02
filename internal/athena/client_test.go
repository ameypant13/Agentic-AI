package athena

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestClientOnlyGetsAllowedViews(t *testing.T) {
	var got []string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Method+" "+r.URL.RequestURI()+" auth="+r.Header.Get("Authorization"))
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	c := New("dGVzdDp0ZXN0").WithHTTPClient(srv.Client())
	ctx := context.Background()

	raw, err := c.Get(ctx, srv.URL, "feature_config", url.Values{"configType": {"sandbox"}, "evil": {"1"}})
	if err != nil || string(raw) != `{"ok":true}` {
		t.Fatalf("get: %s %v", raw, err)
	}
	if want := "GET /athena/api/v1/featureConfig?configType=sandbox auth=dGVzdDp0ZXN0"; got[0] != want {
		t.Errorf("request = %q, want %q", got[0], want)
	}
	for _, view := range []string{"ondemandrestore", "../sandbox/deleteAll", "backup/delete"} {
		if _, err := c.Get(ctx, srv.URL, view, nil); err == nil {
			t.Errorf("view %q allowed", view)
		}
	}
	if len(got) != 1 {
		t.Errorf("unexpected requests: %v", got)
	}
	if _, err := New("").Get(ctx, srv.URL, "cluster", nil); err != ErrNotConfigured {
		t.Errorf("no token: %v", err)
	}
}

func TestBaseURL(t *testing.T) {
	for in, want := range map[string]string{
		"capgroup.app.prophecy.ai":          "https://capgroup.app.prophecy.ai/athena/api/v1",
		"https://capgroup.app.prophecy.ai/": "https://capgroup.app.prophecy.ai/athena/api/v1",
		"https://x.prophecy.ai/some/path":   "https://x.prophecy.ai/athena/api/v1",
	} {
		if got, err := BaseURL(in); err != nil || got != want {
			t.Errorf("%s → %s %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "http://x.prophecy.ai", "https://user:pw@x.prophecy.ai"} {
		if _, err := BaseURL(bad); err == nil || strings.Contains(err.Error(), "pw") {
			t.Errorf("%q: %v", bad, err)
		}
	}
}
