package aiclient

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// ErrNoJSON is returned when a model response contains nothing that could be
// JSON at all.
var ErrNoJSON = errors.New("no JSON object found in model response")

// ExtractJSON pulls the first complete JSON value out of a model response.
//
// This exists because strict structured output is not universally available.
// An Open WebUI deployment proxying to Ollama or vLLM may ignore
// response_format entirely, and smaller models wrap their answer in prose or a
// markdown fence even when told not to. Rather than failing a whole review run
// on presentation, the response is mined for the payload: fenced blocks first,
// then a brace/bracket scan that respects strings and escapes so a JSON string
// containing "}" does not truncate the object.
func ExtractJSON(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", ErrNoJSON
	}

	// Some models emit a reasoning preamble in <think> tags.
	if i := strings.LastIndex(s, "</think>"); i >= 0 {
		s = strings.TrimSpace(s[i+len("</think>"):])
	}

	// A fenced block is the most common wrapper.
	if inner, ok := fencedBlock(s); ok {
		if v, err := scanJSON(inner); err == nil {
			return v, nil
		}
	}
	return scanJSON(s)
}

// fencedBlock returns the contents of the first ``` fence, if any.
func fencedBlock(s string) (string, bool) {
	start := strings.Index(s, "```")
	if start < 0 {
		return "", false
	}
	rest := s[start+3:]
	// Skip an optional language tag on the opening fence.
	if nl := strings.IndexByte(rest, '\n'); nl >= 0 {
		tag := strings.TrimSpace(rest[:nl])
		if len(tag) < 20 && !strings.ContainsAny(tag, "{[\"") {
			rest = rest[nl+1:]
		}
	}
	if end := strings.Index(rest, "```"); end >= 0 {
		return strings.TrimSpace(rest[:end]), true
	}
	return strings.TrimSpace(rest), true
}

// scanJSON finds the first balanced JSON object or array in s, tracking a
// stack of open brackets so mixed nesting is closed correctly.
func scanJSON(s string) (string, error) {
	start := -1
	for i := 0; i < len(s); i++ {
		if s[i] == '{' || s[i] == '[' {
			start = i
			break
		}
	}
	if start < 0 {
		return "", ErrNoJSON
	}

	stack, _, complete := walk(s[start:])
	if complete > 0 {
		return s[start : start+complete], nil
	}
	if len(stack) > 0 {
		// Truncated output - the model hit its token limit mid-object. Close
		// what is open so the complete prefix is still usable, rather than
		// discarding an otherwise good batch of results.
		if repaired := repairTruncated(s[start:]); json.Valid([]byte(repaired)) {
			return repaired, nil
		}
	}
	return "", fmt.Errorf("%w: unbalanced JSON starting at offset %d", ErrNoJSON, start)
}

// walk scans a JSON prefix and reports the stack of still-open brackets,
// whether it ended inside a string, and the length of the first complete
// top-level value (zero when it never closed).
func walk(s string) (stack []byte, inString bool, complete int) {
	escaped := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if escaped {
			escaped = false
			continue
		}
		if inString {
			switch c {
			case '\\':
				escaped = true
			case '"':
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '{', '[':
			stack = append(stack, c)
		case '}', ']':
			if len(stack) == 0 {
				continue
			}
			stack = stack[:len(stack)-1]
			if len(stack) == 0 && complete == 0 {
				return nil, false, i + 1
			}
		}
	}
	return stack, inString, 0
}

// repairTruncated drops the incomplete trailing element and appends the
// brackets needed to close everything still open.
func repairTruncated(s string) string {
	if cut := lastCompleteBoundary(s); cut > 0 {
		s = s[:cut]
	}
	s = strings.TrimRight(s, " \t\n,")

	stack, inString, _ := walk(s)
	var b strings.Builder
	b.WriteString(s)
	if inString {
		b.WriteByte('"')
	}
	for i := len(stack) - 1; i >= 0; i-- {
		if stack[i] == '{' {
			b.WriteByte('}')
		} else {
			b.WriteByte(']')
		}
	}
	return b.String()
}

// lastCompleteBoundary returns the offset just past the last closing bracket
// that was not inside a string.
func lastCompleteBoundary(s string) int {
	inString, escaped := false, false
	last := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if escaped {
			escaped = false
			continue
		}
		if inString {
			switch c {
			case '\\':
				escaped = true
			case '"':
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '}', ']':
			last = i + 1
		}
	}
	return last
}

// UnmarshalLoose extracts JSON from a model response and decodes it into v.
func UnmarshalLoose(response string, v any) error {
	payload, err := ExtractJSON(response)
	if err != nil {
		return err
	}
	if err := json.Unmarshal([]byte(payload), v); err != nil {
		return fmt.Errorf("decode model JSON: %w", err)
	}
	return nil
}
