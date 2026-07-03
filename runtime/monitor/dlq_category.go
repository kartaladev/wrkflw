package monitor

import "strings"

// ClassifyDeadLetter classifies a dead-letter's last error string into a
// coarse category suitable for admin DLQ display. Matching is
// case-insensitive; the first matching rule wins.
//
// Categories returned:
//   - "timeout"    — the error mentions "deadline" or "timeout"
//   - "connection" — the error mentions "connection", "dial", "refused", or "eof"
//   - "validation" — the error mentions "validation" or "invalid"
//   - "unknown"    — no rule matched, or lastError is empty
func ClassifyDeadLetter(lastError string) string {
	lower := strings.ToLower(lastError)
	switch {
	case strings.Contains(lower, "deadline") || strings.Contains(lower, "timeout"):
		return "timeout"
	case strings.Contains(lower, "connection") || strings.Contains(lower, "dial") ||
		strings.Contains(lower, "refused") || strings.Contains(lower, "eof"):
		return "connection"
	case strings.Contains(lower, "validation") || strings.Contains(lower, "invalid"):
		return "validation"
	default:
		return "unknown"
	}
}
