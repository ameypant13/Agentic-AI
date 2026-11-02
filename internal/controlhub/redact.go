package controlhub

import (
	"encoding/json"

	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/redact"
)

// Redact replaces secret-looking values in a JSON document with "[redacted]".
// Control-hub responses carry post-deploy user passwords, tokens and similar;
// none of that may reach the model or the browser. Matching is by key name,
// so it errs on the side of hiding.
func Redact(raw []byte) []byte {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return raw // not JSON; callers only pass control-hub JSON bodies
	}
	out, err := json.Marshal(redactValue(v))
	if err != nil {
		return raw
	}
	return out
}

func redactValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			if redact.IsSecretKey(k) {
				if s, ok := val.(string); ok && s == "" {
					continue
				}
				t[k] = "[redacted]"
				continue
			}
			t[k] = redactValue(val)
		}
		return t
	case []any:
		for i := range t {
			t[i] = redactValue(t[i])
		}
		return t
	default:
		return v
	}
}
