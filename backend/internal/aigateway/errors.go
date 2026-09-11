package aigateway

import (
	"fmt"
	"strings"
)

// providerError wraps a non-200 provider response into a clean, single-line
// error. The raw body may be large or verbose; truncate it so it stays
// useful in logs and in the UI's "test connection" error surface.
func providerError(format string, status int, body []byte) error {
	msg := strings.TrimSpace(string(body))
	const maxLen = 240
	if len(msg) > maxLen {
		msg = msg[:maxLen] + "…"
	}
	if msg == "" {
		msg = fmt.Sprintf("HTTP %d", status)
	}
	return fmt.Errorf("aigateway: %s provider returned %d: %s", format, status, msg)
}
