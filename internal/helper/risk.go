package helper

import (
	"sort"
	"strings"
	"sync/atomic"

	"third-party-review/internal/model"
)

// Pure computations over the score enums. They are functions rather than
// methods because model carries no behaviour, and they live here rather than
// in one service because the review service computes them, the export writes
// them and the templates display them.

// currentMatrix is the risk matrix Band and BandFromFloat compute against.
// It is process-wide mutable state rather than a parameter threaded through
// every caller deliberately: Band/BandFromFloat are called from the template
// funcs, the CSV/Excel exporter, the AI summary prompt builder and the
// dashboard aggregator - four packages that otherwise share nothing - and
// this is a single-tenant app with exactly one matrix for everyone (see
// model.RiskMatrix), so a small piece of shared, swappable state is a better
// fit here than plumbing a settings value through all four call paths.
// main loads the persisted matrix into it at startup; the settings service
// calls SetRiskMatrix again whenever someone saves a change, so every
// already-open page picks up the new mapping on its next render with no
// restart needed.
var currentMatrix atomic.Pointer[model.RiskMatrix]

func init() {
	m := model.DefaultRiskMatrix
	currentMatrix.Store(&m)
}

// SetRiskMatrix installs the matrix Band and BandFromFloat use from now on.
func SetRiskMatrix(m model.RiskMatrix) { currentMatrix.Store(&m) }

// CurrentRiskMatrix returns the matrix currently in effect, for the settings
// page to pre-fill its form with what is actually live.
func CurrentRiskMatrix() model.RiskMatrix { return *currentMatrix.Load() }

// ValidRiskScore reports whether the score is inside the 1-5 scale.
func ValidRiskScore(r model.RiskScore) bool { return r >= model.RiskMin && r <= model.RiskMax }

// ValidRiskMatrix reports whether a risk matrix is usable: every cutoff must
// be inside the 1-5 scale, and non-decreasing so each band is either
// reachable in score order or deliberately collapsed (two equal cutoffs skip
// a band; cutoffs out of order would make one unreachable by accident).
func ValidRiskMatrix(m model.RiskMatrix) bool {
	return ValidRiskScore(m.MediumMin) && ValidRiskScore(m.HighMin) && ValidRiskScore(m.CriticalMin) &&
		m.MediumMin <= m.HighMin && m.HighMin <= m.CriticalMin
}

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

// Band maps a 1-5 score onto a display band, using the currently configured
// risk matrix (see CurrentRiskMatrix). A score of 0 - "not scored" - is
// always Unknown regardless of the matrix, since no cutoff should be able to
// claim an answer the AI never rated.
func Band(r model.RiskScore) model.RiskBand {
	m := CurrentRiskMatrix()
	switch {
	case r <= 0:
		return model.BandUnknown
	case r < m.MediumMin:
		return model.BandLow
	case r < m.HighMin:
		return model.BandMedium
	case r < m.CriticalMin:
		return model.BandHigh
	default:
		return model.BandCritical
	}
}

// BandFromFloat maps an aggregated (fractional) score onto a display band,
// using the same configured matrix as Band. Each integer cutoff is shifted
// down by half a point so a weighted average that lands exactly between two
// whole scores still falls on the side a reviewer would expect - the same
// relationship the original hardcoded bands had (a MediumMin of 3 meant an
// aggregate below 2.5 read as Low).
func BandFromFloat(f float64) model.RiskBand {
	m := CurrentRiskMatrix()
	switch {
	case f <= 0:
		return model.BandUnknown
	case f < float64(m.MediumMin)-0.5:
		return model.BandLow
	case f < float64(m.HighMin)-0.5:
		return model.BandMedium
	case f < float64(m.CriticalMin)-0.5:
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
