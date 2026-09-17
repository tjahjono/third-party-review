package dto

import "github.com/google/uuid"

// This file covers the "revised answers" round trip: a vendor's completed
// questionnaire goes out with assessor feedback, comes back with updated
// third_party_answer values a round or two later, and an assessor re-uploads
// it. There is no stable per-row identifier that survives an export and
// re-upload, so matching is done by domain plus a fuzzy match on the question
// text (see answerMatchThreshold in service/assessment/revise.go) rather than
// by any column in the file.

// AnswerRevisionRow is one row from the re-uploaded file that was matched to
// an existing question in this assessment.
type AnswerRevisionRow struct {
	QuestionID   uuid.UUID `json:"question_id"`
	QuestionText string    `json:"question_text"`
	DomainName   string    `json:"domain_name"`
	OldAnswer    string    `json:"old_answer"`
	NewAnswer    string    `json:"new_answer"`
	// Changed reports whether NewAnswer actually differs from OldAnswer. A
	// matched row with an identical answer is still listed - so the match
	// itself is auditable - but is not counted or pre-checked as a change.
	Changed bool `json:"changed"`
	// MatchScore is the fuzzy-match confidence (0-1) against QuestionText.
	MatchScore float64 `json:"match_score"`
	// SourceRow is the zero-based row index in the re-uploaded file.
	SourceRow int `json:"source_row"`
}

// UnmatchedRevisionRow is a question row from the re-uploaded file that could
// not be confidently matched to any existing question in the assessment. It
// is surfaced rather than silently dropped, so a reworded question or a
// genuinely new row doesn't disappear without a trace.
type UnmatchedRevisionRow struct {
	QuestionText string  `json:"question_text"`
	SourceRow    int     `json:"source_row"`
	BestScore    float64 `json:"best_score"`
}

// AnswerRevisionPreview is the result of matching a re-uploaded questionnaire
// against an assessment's existing questions. Nothing is written to build
// this - it is shown to the user, who confirms which changes to apply.
type AnswerRevisionPreview struct {
	Rows      []AnswerRevisionRow    `json:"rows"`
	Unmatched []UnmatchedRevisionRow `json:"unmatched,omitempty"`
	// ChangedCount is how many of Rows have Changed set, i.e. how many
	// questions would actually be touched if every row were applied.
	ChangedCount int `json:"changed_count"`
}

// AnswerRevisionInput is one confirmed change: the user has reviewed the
// preview and asked for this question's answer to be replaced.
type AnswerRevisionInput struct {
	QuestionID uuid.UUID
	NewAnswer  string
}
