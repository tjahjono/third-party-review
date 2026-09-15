package helper

import (
	"sort"
	"strings"

	"third-party-review/internal/model"
)

// Pure computations over the score enums. They are functions rather than
// methods because model carries no behaviour, and they live here rather than
// in one service because the review service computes them, the export writes
// them and the templates display them.

// ValidRiskScore reports whether the score is inside the 1-5 scale.
func ValidRiskScore(r model.RiskScore) bool { return r >= model.RiskMin && r <= model.RiskMax }

// ClampRiskScore forces a score into the 1-5 range. Models occasionally return
// 0 or 6; clamping is preferable to discarding an otherwise usable review.
func ClampRiskScore(r model.RiskScore) model.RiskScore {
	if r < model.RiskMin {
		return model.RiskMin
	}
	if r > model.RiskMax {
		return model.RiskMax
	}
	return r
}

// Band maps a 1-5 score onto a display band. The mapping is deliberately
// asymmetric: a 5 is called out as Critical because a single such finding
// should be visible in a list of a hundred answers.
func Band(r model.RiskScore) model.RiskBand {
	switch {
	case r <= 0:
		return model.BandUnknown
	case r <= 2:
		return model.BandLow
	case r == 3:
		return model.BandMedium
	case r == 4:
		return model.BandHigh
	default:
		return model.BandCritical
	}
}

// BandFromFloat maps an aggregated (fractional) score onto a display band.
func BandFromFloat(f float64) model.RiskBand {
	switch {
	case f <= 0:
		return model.BandUnknown
	case f < 2.5:
		return model.BandLow
	case f < 3.5:
		return model.BandMedium
	case f < 4.5:
		return model.BandHigh
	default:
		return model.BandCritical
	}
}

// ValidCompleteness reports whether c is a known completeness value.
func ValidCompleteness(c model.Completeness) bool {
	switch c {
	case model.CompletenessComplete, model.CompletenessPartial, model.CompletenessMissing,
		model.CompletenessNonResponsive, model.CompletenessUnknown:
		return true
	}
	return false
}

// Incomplete reports whether the answer needs chasing with the vendor.
func Incomplete(c model.Completeness) bool {
	return c == model.CompletenessPartial ||
		c == model.CompletenessMissing ||
		c == model.CompletenessNonResponsive
}

// ResultBand is the display band for a result's score, nil-safe because the
// templates reach for it on questions that were never reviewed.
func ResultBand(r *model.ReviewResult) model.RiskBand {
	if r == nil {
		return model.BandUnknown
	}
	return Band(r.RiskScore)
}

// HasFlag reports whether a flag of the given kind is present on a result.
func HasFlag(r *model.ReviewResult, k model.FlagKind) bool {
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

// NormalizeResult clamps and defaults the fields a model may have returned out
// of range, so a slightly malformed but usable response is still persisted.
func NormalizeResult(r *model.ReviewResult) {
	if r == nil {
		return
	}
	r.RiskScore = ClampRiskScore(r.RiskScore)
	if !ValidCompleteness(r.Completeness) {
		r.Completeness = model.CompletenessUnknown
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
			f.Severity = ClampRiskScore(f.Severity)
		}
		kept = append(kept, f)
	}
	r.Flags = kept
}

// DomainBand is the display band for one domain's weighted score.
func DomainBand(d model.DomainScore) model.RiskBand { return BandFromFloat(d.WeightedScore) }

// SummaryBand is the display band for an assessment's overall score.
func SummaryBand(s *model.AssessmentSummary) model.RiskBand {
	if s == nil {
		return model.BandUnknown
	}
	return BandFromFloat(s.OverallScore)
}

// PercentFinalized reports human sign-off progress as a whole percentage.
func PercentFinalized(s *model.AssessmentSummary) int {
	if s == nil || s.QuestionCount == 0 {
		return 0
	}
	return int(float64(s.FinalizedCount) / float64(s.QuestionCount) * 100)
}

// SortDomainScores orders domain scores by their seeded sort order so the
// summary always reads in the canonical TPSA sequence.
func SortDomainScores(ds []model.DomainScore) {
	sort.SliceStable(ds, func(i, j int) bool { return ds[i].SortOrder < ds[j].SortOrder })
}
