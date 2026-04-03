// Package jsonutil provides JSON utilities with consistent formatting.
package jsonutil

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// ExtractJSONObject finds the first top-level JSON object in s by matching
// braces. Returns empty string if no balanced object is found. Useful for
// extracting JSON from LLM responses that include trailing explanation text.
func ExtractJSONObject(s string) string {
	start := strings.IndexByte(s, '{')
	if start < 0 {
		return ""
	}
	depth := 0
	inString := false
	escaped := false
	for i := start; i < len(s); i++ {
		if escaped {
			escaped = false
			continue
		}
		c := s[i]
		switch {
		case c == '\\' && inString:
			escaped = true
		case c == '"':
			inString = !inString
		case !inString && c == '{':
			depth++
		case !inString && c == '}':
			depth--
			if depth == 0 {
				return s[start : i+1]
			}
		}
	}
	return ""
}

// MarshalIndentWithNewline is like json.MarshalIndent but adds a trailing newline.
// This ensures JSON files have proper POSIX line endings.
func MarshalIndentWithNewline(v any, prefix, indent string) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent(prefix, indent)
	if err := enc.Encode(v); err != nil {
		return nil, fmt.Errorf("encoding JSON: %w", err)
	}
	return buf.Bytes(), nil
}
