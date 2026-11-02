// Package redact masks secrets in text the console shows or sends to the model.
package redact

import (
	"regexp"
	"strings"
)

// IsSecretKey reports whether a JSON key names a secret value. IDs that merely
// point at a secret (systemUserTokenId) are kept: they are useful and harmless.
func IsSecretKey(k string) bool {
	l := strings.ToLower(k)
	if strings.HasSuffix(l, "id") || strings.HasSuffix(l, "ids") {
		return false
	}
	for _, s := range []string{"password", "passwd", "secret", "privatekey", "private_key",
		"credential", "apikey", "api_key", "kubeconfig", "token", "certificatedata", "keydata", "authorization"} {
		if strings.Contains(l, s) {
			return true
		}
	}
	return false
}

var (
	jwtRe    = regexp.MustCompile(`eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}`)
	bearerRe = regexp.MustCompile(`(?i)(bearer|basic)\s+[A-Za-z0-9._~+/=-]{8,}`)
	kvRe     = regexp.MustCompile(`(?i)("?[a-z_]*(password|secret|token|apikey|api_key|credential)[a-z_]*"?\s*[:=]\s*)("[^"]*"|[^\s,}]+)`)
)

// LogLine masks tokens and secret-looking key=value pairs.
func LogLine(s string) string {
	s = jwtRe.ReplaceAllString(s, "[redacted-jwt]")
	s = bearerRe.ReplaceAllString(s, "$1 [redacted]")
	return kvRe.ReplaceAllString(s, "${1}[redacted]")
}

// Text masks every line of a multi-line value (e.g. a config file in a ConfigMap).
func Text(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = LogLine(l)
	}
	return strings.Join(lines, "\n")
}
