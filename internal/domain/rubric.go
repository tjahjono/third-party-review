package domain

import (
	"strings"
	"time"
)

// Rubric is a policy or checklist the AI compares answers against. It may be
// attached to a single assessment or marked reusable across assessments.
type Rubric struct {
	ID   int64
	Name string
	// Content is free text: a pasted policy, a checklist, or structured
	// criteria. It is passed to the AI verbatim (truncated to fit context).
	Content string
	// Reusable rubrics appear in the picker on every assessment.
	Reusable  bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Validate checks the rubric is safe to persist.
func (r *Rubric) Validate() error {
	var errs ValidationErrors
	r.Name = strings.TrimSpace(r.Name)
	r.Content = strings.TrimSpace(r.Content)
	if r.Name == "" {
		errs.Add("name", "Give the rubric a name so you can pick it later.")
	}
	if r.Content == "" {
		errs.Add("content", "Rubric content cannot be empty.")
	}
	return errs.OrNil()
}

// Excerpt returns at most maxRunes of the rubric content, cut on a line
// boundary where possible, for inclusion in an AI prompt.
func (r *Rubric) Excerpt(maxRunes int) string {
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
