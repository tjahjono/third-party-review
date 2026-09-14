package aiclient

import (
	"context"
	"fmt"
	"strings"

	"third-party-review/internal/domain"
)

// Mock is a deterministic AIReviewer used by tests and by local development
// without an API key. It applies a handful of the heuristics a real reviewer
// would, so the rest of the pipeline - scoring, aggregation, flags, drafts,
// progress - can be exercised end to end and asserted on. It is not a
// substitute for a model: it does not read for meaning.
type Mock struct {
	// FailOn, when set, makes ReviewBatch return an error for any batch that
	// contains a question whose text contains this string. Used to test the
	// per-question fallback path and job failure handling.
	FailOn string
	// SkipQuestionIDs are omitted from batch responses, simulating a model
	// that silently drops items.
	SkipQuestionIDs map[int64]bool
}

// NewMock constructs the mock reviewer.
func NewMock() *Mock { return &Mock{} }

// Name identifies the provider on persisted results.
func (m *Mock) Name() string { return "mock" }

// vagueMarkers are the phrases a human assessor learns to distrust.
var vagueMarkers = []string{
	"industry standard", "best practice", "as per policy", "where appropriate",
	"as required", "standard controls", "we follow", "n/a", "not applicable",
	"yes", "no comment",
}

// ReviewAnswer scores one answer with simple, explainable rules.
func (m *Mock) ReviewAnswer(_ context.Context, req domain.ReviewRequest) (domain.ReviewResult, error) {
	return m.score(req.Question), nil
}

// ReviewBatch scores every question in the batch.
func (m *Mock) ReviewBatch(_ context.Context, req domain.BatchReviewRequest) (domain.BatchReviewResponse, error) {
	if m.FailOn != "" {
		for _, q := range req.Questions {
			if strings.Contains(q.QuestionText, m.FailOn) {
				return domain.BatchReviewResponse{}, fmt.Errorf("mock: induced failure on %q", m.FailOn)
			}
		}
	}
	out := domain.BatchReviewResponse{Results: map[int64]domain.ReviewResult{}}
	for _, q := range req.Questions {
		if m.SkipQuestionIDs[q.QuestionID] {
			continue
		}
		out.Results[q.QuestionID] = m.score(q)
	}
	if len(req.Questions) > 1 {
		out.Notes = []string{fmt.Sprintf("Mock reviewer evaluated %d answers together.", len(req.Questions))}
	}
	return out, nil
}

// Summarize writes a deterministic narrative from the supplied aggregates.
func (m *Mock) Summarize(_ context.Context, req domain.SummaryRequest) (string, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "%s presents %s residual risk overall, scoring %.2f out of 5 across %d reviewed answers. ",
		orDash(req.VendorName), strings.ToLower(domain.BandFromFloat(req.OverallScore).Label()),
		req.OverallScore, req.QuestionCount)
	fmt.Fprintf(&b, "%d answer(s) were flagged and %d were missing or incomplete.\n\n",
		req.FlaggedCount, req.IncompleteCount)

	if len(req.DomainScores) > 0 {
		worst := req.DomainScores[0]
		for _, d := range req.DomainScores {
			if d.WeightedScore > worst.WeightedScore {
				worst = d
			}
		}
		fmt.Fprintf(&b, "%s carries the highest residual risk at %.2f out of 5. ",
			worst.DomainName, worst.WeightedScore)
	}
	b.WriteString("Resolve the flagged and incomplete answers with the vendor before approval.\n\n")
	b.WriteString("This narrative was produced by the mock reviewer and is not a model-generated assessment.")
	return b.String(), nil
}

// score applies the rule set. It mirrors the scale the real system prompt
// describes so fixtures and expectations stay consistent across providers.
func (m *Mock) score(q domain.QuestionContext) domain.ReviewResult {
	answer := strings.TrimSpace(q.ThirdPartyAnswer)
	lower := strings.ToLower(answer)

	res := domain.ReviewResult{
		QuestionID: q.QuestionID,
		Provider:   m.Name(),
		Model:      "mock",
		Confidence: 0.5,
	}

	switch {
	case answer == "":
		res.RiskScore = 5
		res.Completeness = domain.CompletenessMissing
		res.Flags = append(res.Flags, domain.Flag{
			Kind:   domain.FlagMissingAnswer,
			Detail: "The vendor left this question unanswered.",
		})
		res.Rationale = "No answer was provided."
		res.FeedbackDraft = fmt.Sprintf(
			"The vendor did not answer this %s question. No control can be evidenced from a blank response. Request a written answer describing the control, its scope and how often it is reviewed.",
			q.DomainName)
		res.Confidence = 0.95

	case isVague(lower):
		res.RiskScore = 4
		res.Completeness = domain.CompletenessPartial
		res.Flags = append(res.Flags, domain.Flag{
			Kind:   domain.FlagVague,
			Detail: "The answer asserts a control without naming it or describing its scope.",
		})
		res.Rationale = "The answer relies on generic assurance language rather than a specific control."
		res.FeedbackDraft = fmt.Sprintf(
			"The vendor's response to this %s question asserts that controls exist but does not name them, state their scope, or say how often they are reviewed. As written the control cannot be relied upon. Ask the vendor to describe the specific control and its review cadence.",
			q.DomainName)
		res.Confidence = 0.6

	case len([]rune(answer)) < 40:
		res.RiskScore = 3
		res.Completeness = domain.CompletenessPartial
		res.Rationale = "The answer is brief and leaves material detail unstated."
		res.FeedbackDraft = fmt.Sprintf(
			"The vendor gave a short answer to this %s question that leaves the detail of the control unstated. Ask for the specifics: what is implemented, over what scope, and how it is verified.",
			q.DomainName)

	default:
		res.RiskScore = 2
		res.Completeness = domain.CompletenessComplete
		res.Rationale = "The answer describes a specific control."
		res.FeedbackDraft = fmt.Sprintf(
			"The vendor describes a specific control in response to this %s question and the answer is responsive to what was asked. No further information is required at this stage.",
			q.DomainName)
		res.Confidence = 0.7
	}

	// Absent evidence on an otherwise adequate claim is worth a note, not a
	// higher score - the same judgement the real prompt asks for.
	if !q.HasEvidence && res.RiskScore <= 3 {
		res.Flags = append(res.Flags, domain.Flag{
			Kind:   domain.FlagEvidenceAbsent,
			Detail: "No supporting evidence reference was supplied for this claim.",
		})
	}

	res.Normalize()
	return res
}

func isVague(lowerAnswer string) bool {
	if lowerAnswer == "" {
		return false
	}
	for _, marker := range vagueMarkers {
		if strings.Contains(lowerAnswer, marker) && len([]rune(lowerAnswer)) < 160 {
			return true
		}
	}
	return false
}

var _ domain.AIReviewer = (*Mock)(nil)
