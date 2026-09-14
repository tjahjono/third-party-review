package domain

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// RiskScore is the per-answer residual risk on a 1-5 scale, where 1 is the
// lowest risk. The UI displays a band derived from it (see Band).
type RiskScore int

const (
	RiskMin RiskScore = 1
	RiskMax RiskScore = 5
)

// Valid reports whether the score is inside the 1-5 scale.
func (r RiskScore) Valid() bool { return r >= RiskMin && r <= RiskMax }

// Clamp forces a score into the 1-5 range. Models occasionally return 0 or 6;
// clamping is preferable to discarding an otherwise usable review.
func (r RiskScore) Clamp() RiskScore {
	if r < RiskMin {
		return RiskMin
	}
	if r > RiskMax {
		return RiskMax
	}
	return r
}

// RiskBand is the coarse label shown in the UI, derived from the numeric score.
type RiskBand string

const (
	BandLow      RiskBand = "low"
	BandMedium   RiskBand = "medium"
	BandHigh     RiskBand = "high"
	BandCritical RiskBand = "critical"
	BandUnknown  RiskBand = "unknown"
)

// Label is the human-readable band name.
func (b RiskBand) Label() string {
	switch b {
	case BandLow:
		return "Low"
	case BandMedium:
		return "Medium"
	case BandHigh:
		return "High"
	case BandCritical:
		return "Critical"
	}
	return "Not scored"
}

// Band maps a 1-5 score onto a display band. The mapping is deliberately
// asymmetric: a 5 is called out as Critical because a single such finding
// should be visible in a list of a hundred answers.
func (r RiskScore) Band() RiskBand {
	switch {
	case r <= 0:
		return BandUnknown
	case r <= 1:
		return BandLow
	case r == 2:
		return BandLow
	case r == 3:
		return BandMedium
	case r == 4:
		return BandHigh
	default:
		return BandCritical
	}
}

// BandFromFloat maps an aggregated (fractional) score onto a display band.
func BandFromFloat(f float64) RiskBand {
	switch {
	case f <= 0:
		return BandUnknown
	case f < 2.5:
		return BandLow
	case f < 3.5:
		return BandMedium
	case f < 4.5:
		return BandHigh
	default:
		return BandCritical
	}
}

// Completeness describes whether the vendor actually answered the question.
type Completeness string

const (
	CompletenessComplete      Completeness = "complete"
	CompletenessPartial       Completeness = "partial"
	CompletenessMissing       Completeness = "missing"
	CompletenessNonResponsive Completeness = "non_responsive"
	CompletenessUnknown       Completeness = "unknown"
)

// Valid reports whether c is a known completeness value.
func (c Completeness) Valid() bool {
	switch c {
	case CompletenessComplete, CompletenessPartial, CompletenessMissing,
		CompletenessNonResponsive, CompletenessUnknown:
		return true
	}
	return false
}

// Label is the human-readable form shown in the UI.
func (c Completeness) Label() string {
	switch c {
	case CompletenessComplete:
		return "Complete"
	case CompletenessPartial:
		return "Partial"
	case CompletenessMissing:
		return "Missing"
	case CompletenessNonResponsive:
		return "Not responsive"
	}
	return "Unknown"
}

// Incomplete reports whether the answer needs chasing with the vendor.
func (c Completeness) Incomplete() bool {
	return c == CompletenessPartial || c == CompletenessMissing || c == CompletenessNonResponsive
}

// FlagKind classifies why an answer was flagged.
type FlagKind string

const (
	FlagInconsistency  FlagKind = "inconsistency"
	FlagSecurityRisk   FlagKind = "security_risk"
	FlagRubricGap      FlagKind = "rubric_gap"
	FlagMissingAnswer  FlagKind = "missing_answer"
	FlagEvidenceAbsent FlagKind = "evidence_absent"
	FlagVague          FlagKind = "vague"
)

// Label is the human-readable form shown in the UI.
func (f FlagKind) Label() string {
	switch f {
	case FlagInconsistency:
		return "Inconsistent with another answer"
	case FlagSecurityRisk:
		return "Security concern"
	case FlagRubricGap:
		return "Gap against rubric"
	case FlagMissingAnswer:
		return "No answer provided"
	case FlagEvidenceAbsent:
		return "No supporting evidence"
	case FlagVague:
		return "Vague or unverifiable"
	}
	return string(f)
}

// Flag is a single reviewer-visible concern raised against an answer.
type Flag struct {
	Kind     FlagKind  `json:"kind"`
	Detail   string    `json:"detail"`
	Severity RiskScore `json:"severity,omitempty"`
	// RelatedQuestionIDs points at the other answers an inconsistency flag
	// contradicts, when the AI identified them.
	RelatedQuestionIDs []int64 `json:"related_question_ids,omitempty"`
}

// ReviewResult is one AI evaluation of one question. Results are kept as child
// rows keyed by RunID rather than overwriting the Question, so re-reviews
// preserve history.
type ReviewResult struct {
	ID         int64
	QuestionID int64
	RunID      int64

	RiskScore    RiskScore
	Completeness Completeness
	Flags        []Flag
	Rationale    string
	// FeedbackDraft is the prose written in the voice of a security assessor.
	FeedbackDraft string
	// Confidence is the model's self-reported confidence in [0,1]. Low values
	// drive the per-question follow-up pass.
	Confidence float64

	Provider string
	Model    string
	// Raw is the unparsed model response, retained for debugging a bad review.
	Raw       string
	CreatedAt time.Time
}

// HasFlag reports whether a flag of the given kind is present.
func (r *ReviewResult) HasFlag(k FlagKind) bool {
	if r == nil {
		return false
	}
	for _, f := range r.Flags {
		if f.Kind == k {
			return true
		}
	}
	return false
}

// Band is the display band for this result's score.
func (r *ReviewResult) Band() RiskBand {
	if r == nil {
		return BandUnknown
	}
	return r.RiskScore.Band()
}

// Normalize clamps and defaults the fields a model may have returned out of
// range, so a slightly malformed but usable response is still persisted.
func (r *ReviewResult) Normalize() {
	r.RiskScore = r.RiskScore.Clamp()
	if !r.Completeness.Valid() {
		r.Completeness = CompletenessUnknown
	}
	if r.Confidence < 0 {
		r.Confidence = 0
	}
	if r.Confidence > 1 {
		r.Confidence = 1
	}
	r.Rationale = strings.TrimSpace(r.Rationale)
	r.FeedbackDraft = strings.TrimSpace(r.FeedbackDraft)
	kept := r.Flags[:0]
	for _, f := range r.Flags {
		f.Detail = strings.TrimSpace(f.Detail)
		if f.Kind == "" {
			continue
		}
		if f.Severity != 0 {
			f.Severity = f.Severity.Clamp()
		}
		kept = append(kept, f)
	}
	r.Flags = kept
}

// DomainScore is the aggregated risk for one domain within an assessment.
type DomainScore struct {
	DomainID        int64
	DomainName      string
	SortOrder       int
	QuestionCount   int
	ScoredCount     int
	MeanScore       float64
	WorstScore      RiskScore
	WeightedScore   float64
	FlaggedCount    int
	IncompleteCount int
}

// Band is the display band for this domain's weighted score.
func (d DomainScore) Band() RiskBand { return BandFromFloat(d.WeightedScore) }

// AssessmentSummary is the aggregated result for a whole assessment.
type AssessmentSummary struct {
	AssessmentID int64
	RunID        *int64

	QuestionCount int
	ScoredCount   int
	// OverallScore is the weighted worst-case aggregate on the 1-5 scale.
	OverallScore float64
	MeanScore    float64
	WorstScore   RiskScore

	FlaggedCount        int
	IncompleteCount     int
	PendingFinalization int
	FinalizedCount      int

	DomainScores []DomainScore
	// Narrative is the AI's high-level summary of the assessment.
	Narrative string

	GeneratedAt time.Time
}

// Band is the display band for the overall score.
func (s *AssessmentSummary) Band() RiskBand {
	if s == nil {
		return BandUnknown
	}
	return BandFromFloat(s.OverallScore)
}

// PercentFinalized reports human sign-off progress as a whole percentage.
func (s *AssessmentSummary) PercentFinalized() int {
	if s == nil || s.QuestionCount == 0 {
		return 0
	}
	return int(float64(s.FinalizedCount) / float64(s.QuestionCount) * 100)
}

// String renders the overall score for logs and CSV exports.
func (s *AssessmentSummary) String() string {
	if s == nil {
		return "no summary"
	}
	return fmt.Sprintf("%.2f/5 (%s), %d flagged, %d incomplete, %d/%d finalized",
		s.OverallScore, s.Band().Label(), s.FlaggedCount, s.IncompleteCount,
		s.FinalizedCount, s.QuestionCount)
}

// SortDomainScores orders domain scores by their seeded sort order so the
// summary always reads in the canonical TPSA sequence.
func SortDomainScores(ds []DomainScore) {
	sort.SliceStable(ds, func(i, j int) bool { return ds[i].SortOrder < ds[j].SortOrder })
}
