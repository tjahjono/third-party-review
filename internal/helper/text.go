package helper

import (
	"strings"

	"third-party-review/internal/model"
)

// NormalizeHeader lowercases, collapses whitespace and strips punctuation
// commonly found in spreadsheet headers so aliases can be compared reliably.
func NormalizeHeader(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	b.Grow(len(s))
	lastSpace := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastSpace = false
		default:
			if !lastSpace && b.Len() > 0 {
				b.WriteByte(' ')
				lastSpace = true
			}
		}
	}
	return strings.TrimSpace(b.String())
}

// RubricExcerpt returns at most maxRunes of the rubric content, cut on a line
// boundary where possible, for inclusion in an AI prompt.
func RubricExcerpt(r *model.Rubric, maxRunes int) string {
	if r == nil {
		return ""
	}
	runes := []rune(r.Content)
	if len(runes) <= maxRunes {
		return r.Content
	}
	cut := string(runes[:maxRunes])
	if i := strings.LastIndex(cut, "\n"); i > maxRunes/2 {
		cut = cut[:i]
	}
	return cut + "\n[rubric truncated]"
}

// HasEvidence reports whether a supporting-evidence reference was supplied.
// Presence alone is a useful signal; the link is never fetched (v1 non-goal).
func HasEvidence(q *model.Question) bool {
	return q != nil && strings.TrimSpace(q.LinkEvidence) != ""
}

// AnswerIsBlank reports whether the vendor left the answer empty. This is a
// cheap pre-check done before the AI call so obviously missing answers are
// flagged even if the AI is unavailable.
func AnswerIsBlank(q *model.Question) bool {
	return q == nil || strings.TrimSpace(q.ThirdPartyAnswer) == ""
}

// EffectiveFeedback returns the text that exports and summaries should use:
// the finalized feedback when signed off, otherwise the AI draft. The second
// return value reports whether the text is still an unconfirmed draft.
func EffectiveFeedback(q *model.Question) (text string, unconfirmed bool) {
	if q == nil {
		return "", true
	}
	if q.ReviewStatus == model.ReviewFinalized && strings.TrimSpace(q.AssessorFeedbackFinal) != "" {
		return q.AssessorFeedbackFinal, false
	}
	if strings.TrimSpace(q.AssessorFeedbackFinal) != "" {
		return q.AssessorFeedbackFinal, true
	}
	return q.AssessorFeedbackDraft, true
}

// SeededDomainNames lists the 8 domains inserted by the initial migration. It
// exists for tests and fixtures; production code reads the table.
var SeededDomainNames = []string{
	"Network Security",
	"Application Security",
	"Logical Access Security",
	"Data Security",
	"Security Logging and Monitoring",
	"Change, Performance and Capacity Management",
	"Cloud Security",
	"AI Security",
}

// MappedColumn returns the source column index a field is mapped to, and
// whether it is mapped at all. It is nil-safe because the mapping screen asks
// about fields on a preview that may have none.
func MappedColumn(m *model.ColumnMapping, f model.QuestionField) (int, bool) {
	if m == nil || m.Bindings == nil {
		return 0, false
	}
	b, ok := m.Bindings[f]
	return b.Index, ok
}
