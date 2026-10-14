package audit

import (
	"regexp"
	"strings"
)

// Redacted replaces every secret value in the details of an entry.
const Redacted = "[REDACTED]"

// sensitiveKeys are the fragments that mark a details key as secret. A key is lower-cased
// and stripped of "_", "-", "." and spaces before the (substring) match, so "API-Key",
// "client_secret" and "refreshToken" are all caught. The price of substring matching is that
// "token_id" is redacted too: the token id is in the actor field.
var sensitiveKeys = []string{
	"password", "passwd", "passphrase", "secret", "token", "authorization", "apikey",
	"privatekey", "credential", "cookie", "bearer", "session",
}

// secretValues catches secrets that leak under an innocent key: an API token, a JWT, an
// "Authorization" header value.
var secretValues = regexp.MustCompile(`(?i)trica_[0-9]+_[A-Za-z0-9_-]{20,}|eyJ[A-Za-z0-9_-]{5,}\.[A-Za-z0-9_-]{5,}\.[A-Za-z0-9_-]*|bearer\s+\S+`)

const maxRedactDepth = 16

func sensitive(key string) bool {
	k := strings.NewReplacer("_", "", "-", "", ".", "", " ", "").Replace(strings.ToLower(key))
	for _, frag := range sensitiveKeys {
		if strings.Contains(k, frag) {
			return true
		}
	}
	return false
}

// Redact returns a copy of v (a value decoded from JSON: maps, slices, strings, numbers)
// with secrets replaced: the value of any sensitive key, and any string that looks like a
// token. Past maxRedactDepth the subtree is dropped rather than trusted.
func Redact(v any) any { return redact(v, 0) }

func redact(v any, depth int) any {
	if depth > maxRedactDepth {
		return Redacted
	}
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			if sensitive(k) {
				out[k] = Redacted
				continue
			}
			out[k] = redact(e, depth+1)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = redact(e, depth+1)
		}
		return out
	case string:
		return secretValues.ReplaceAllString(x, Redacted)
	default:
		return v
	}
}
