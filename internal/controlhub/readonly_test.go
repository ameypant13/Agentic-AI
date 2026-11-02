package controlhub

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestReadOnlyTokenBlocksWrites(t *testing.T) {
	var methods []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method+" "+r.Header.Get("Authorization"))
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	c := New(srv.URL).WithReadOnlyTokens("svc")
	ctx := context.Background()

	if _, err := c.Get(ctx, "svc", "/api/control-planes", nil); err != nil {
		t.Fatalf("GET with read-only token: %v", err)
	}
	if _, err := c.Post(ctx, "svc", "/api/control-planes/x/postdeploy", nil); !errors.Is(err, ErrReadOnlyToken) {
		t.Fatalf("POST with read-only token: err = %v, want ErrReadOnlyToken", err)
	}
	if _, err := c.Post(ctx, "user", "/api/control-planes/x/postdeploy", nil); err != nil {
		t.Fatalf("POST with a user token: %v", err)
	}
	want := []string{"GET Bearer svc", "POST Bearer user"}
	if len(methods) != len(want) || methods[0] != want[0] || methods[1] != want[1] {
		t.Fatalf("requests reaching control-hub = %v, want %v", methods, want)
	}
}
