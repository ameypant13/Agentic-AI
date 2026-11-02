package kube

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"
)

func obj(t *testing.T, y string) *unstructured.Unstructured {
	t.Helper()
	u := &unstructured.Unstructured{}
	if err := yaml.Unmarshal([]byte(y), &u.Object); err != nil {
		t.Fatal(err)
	}
	return u
}

func TestMaskObject(t *testing.T) {
	secret := obj(t, `
apiVersion: v1
kind: Secret
metadata: {name: db, annotations: {kubectl.kubernetes.io/last-applied-configuration: "{\"data\":\"aHVudGVyMg==\"}"}, managedFields: [{manager: kubectl}]}
data: {password: aHVudGVyMg==}
stringData: {token: plain-secret}
`)
	MaskObject(secret)
	out, _ := yaml.Marshal(secret.Object)
	for _, leak := range []string{"aHVudGVyMg", "plain-secret", "managedFields", "last-applied"} {
		if strings.Contains(string(out), leak) {
			t.Errorf("secret leaks %q:\n%s", leak, out)
		}
	}
	if !strings.Contains(string(out), "[hidden: 12 bytes]") {
		t.Errorf("secret keys should stay listed with sizes:\n%s", out)
	}

	cm := obj(t, `
apiVersion: v1
kind: ConfigMap
metadata: {name: app}
data:
  DB_PASSWORD: hunter2
  LOG_LEVEL: info
  application.conf: |
    db.url = "jdbc:postgresql://pg:5432/meta"
    db.password = "s3cret"
`)
	MaskObject(cm)
	out, _ = yaml.Marshal(cm.Object)
	for _, leak := range []string{"hunter2", "s3cret"} {
		if strings.Contains(string(out), leak) {
			t.Errorf("configmap leaks %q:\n%s", leak, out)
		}
	}
	for _, kept := range []string{"LOG_LEVEL: info", "jdbc:postgresql://pg:5432/meta"} {
		if !strings.Contains(string(out), kept) {
			t.Errorf("configmap lost %q:\n%s", kept, out)
		}
	}

	dep := obj(t, `
apiVersion: apps/v1
kind: Deployment
metadata: {name: metagraph}
spec: {template: {spec: {containers: [{name: metagraph, env: [{name: API_TOKEN, value: tok-123}, {name: MODE, value: prod}]}]}}}
`)
	MaskObject(dep)
	out, _ = yaml.Marshal(dep.Object)
	if strings.Contains(string(out), "tok-123") || !strings.Contains(string(out), "value: prod") {
		t.Errorf("deployment env masking wrong:\n%s", out)
	}
}

func TestDecodeHelmReleaseSkipsValues(t *testing.T) {
	rel := `{"name":"prophecy","version":7,"info":{"status":"deployed","last_deployed":"2026-10-01T10:00:00Z","description":"Upgrade complete"},
		"chart":{"metadata":{"name":"prophecy","version":"4.2.11.2","appVersion":"4.2.11.2"}},"config":{"postgres":{"password":"hunter2"}}}`
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	_, _ = zw.Write([]byte(rel))
	_ = zw.Close()
	data := []byte(base64.StdEncoding.EncodeToString(gz.Bytes()))

	got, err := decodeHelmRelease(data)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "prophecy" || got.Revision != 7 || got.Status != "deployed" || got.ChartVersion != "4.2.11.2" {
		t.Errorf("release = %+v", got)
	}
	if strings.Contains(strings.Join([]string{got.Description, got.Chart, got.AppVersion}, " "), "hunter2") {
		t.Error("values leaked into the release view")
	}
}

func TestParseKey(t *testing.T) {
	gvr, err := ParseKey("core/v1/pods")
	if err != nil || gvr.Group != "" || gvr.Resource != "pods" {
		t.Errorf("core key = %+v %v", gvr, err)
	}
	if path := resourcePath(gvr, "prophecy", "x"); path != "/api/v1/namespaces/prophecy/pods/x" {
		t.Errorf("path = %s", path)
	}
	gvr, _ = ParseKey("apps/v1/deployments")
	if path := resourcePath(gvr, "", ""); path != "/apis/apps/v1/deployments" {
		t.Errorf("path = %s", path)
	}
	if _, err := ParseKey("pods"); err == nil {
		t.Error("bad key accepted")
	}
}
