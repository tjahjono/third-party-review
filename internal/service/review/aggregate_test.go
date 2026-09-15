package review

import (
	"math"
	"testing"

	"third-party-review/internal/helper"
	"third-party-review/internal/model"

	"github.com/google/uuid"
)

// uid turns a small test number into a stable uuid, so a test can still say
// "question 3" while the code under test sees real uuids. The bytes are
// deterministic, which keeps failure output readable.
func uid(n int) uuid.UUID {
	var u uuid.UUID
	u[0] = 0x7e
	u[14] = byte(n >> 8)
	u[15] = byte(n)
	return u
}

func q(id, domainID uuid.UUID, name string, status model.ReviewStatus) *model.Question {
	return &model.Question{ID: id, DomainID: domainID, DomainName: name, ReviewStatus: status}
}

func res(qid uuid.UUID, score model.RiskScore, c model.Completeness, flags ...model.Flag) *model.ReviewResult {
	return &model.ReviewResult{QuestionID: qid, RiskScore: score, Completeness: c, Flags: flags}
}

// The behaviour this whole scheme exists for: a handful of serious findings
// among many good answers must not average away into "low risk".
func TestWeightedAggregateDoesNotDiluteCriticalFindings(t *testing.T) {
	var questions []*model.Question
	results := map[uuid.UUID]*model.ReviewResult{}

	// 17 strong answers.
	for i := 1; i <= 17; i++ {
		questions = append(questions, q(uid(i), uid(1000), "Network Security", model.ReviewAIDrafted))
		results[uid(i)] = res(uid(i), 1, model.CompletenessComplete)
	}
	// 3 serious gaps.
	for i := 18; i <= 20; i++ {
		questions = append(questions, q(uid(i), uid(1000), "Network Security", model.ReviewAIDrafted))
		results[uid(i)] = res(uid(i), 5, model.CompletenessMissing,
			model.Flag{Kind: model.FlagMissingAnswer, Detail: "unanswered"})
	}

	s := Aggregate(questions, results)

	if math.Abs(s.MeanScore-1.6) > 0.01 {
		t.Errorf("MeanScore = %.2f, want 1.60", s.MeanScore)
	}
	// The plain mean would band this Low, which is the failure mode.
	if helper.BandFromFloat(s.MeanScore) != model.BandLow {
		t.Fatalf("precondition: the plain mean should band Low, got %s", helper.BandFromFloat(s.MeanScore))
	}
	if s.OverallScore <= s.MeanScore {
		t.Errorf("OverallScore (%.2f) must exceed the plain mean (%.2f)", s.OverallScore, s.MeanScore)
	}
	if got := helper.BandFromFloat(s.OverallScore); got == model.BandLow {
		t.Errorf("OverallScore %.2f bands as %s; three unanswered questions must not read as low risk",
			s.OverallScore, got)
	}
	if s.WorstScore != 5 {
		t.Errorf("WorstScore = %d, want 5", s.WorstScore)
	}
	if s.FlaggedCount != 3 {
		t.Errorf("FlaggedCount = %d, want 3", s.FlaggedCount)
	}
	if s.IncompleteCount != 3 {
		t.Errorf("IncompleteCount = %d, want 3", s.IncompleteCount)
	}
}

func TestAggregateUniformScores(t *testing.T) {
	var questions []*model.Question
	results := map[uuid.UUID]*model.ReviewResult{}
	for i := 1; i <= 10; i++ {
		questions = append(questions, q(uid(i), uid(1000), "Data Security", model.ReviewAIDrafted))
		results[uid(i)] = res(uid(i), 3, model.CompletenessComplete)
	}
	s := Aggregate(questions, results)
	// With no spread the weighted score must equal the mean exactly.
	if s.OverallScore != 3 || s.MeanScore != 3 {
		t.Errorf("uniform scores: overall = %.2f, mean = %.2f, want 3.00 for both", s.OverallScore, s.MeanScore)
	}
	if helper.DomainBand(s.DomainScores[0]) != model.BandMedium {
		t.Errorf("band = %s, want Medium", helper.DomainBand(s.DomainScores[0]))
	}
}

func TestAggregatePerDomainSeparation(t *testing.T) {
	questions := []*model.Question{
		q(uid(1), uid(1001), "Network Security", model.ReviewAIDrafted),
		q(uid(2), uid(1001), "Network Security", model.ReviewAIDrafted),
		q(uid(3), uid(1002), "AI Security", model.ReviewAIDrafted),
		q(uid(4), uid(1002), "AI Security", model.ReviewAIDrafted),
	}
	results := map[uuid.UUID]*model.ReviewResult{
		uid(1): res(uid(1), 1, model.CompletenessComplete),
		uid(2): res(uid(2), 2, model.CompletenessComplete),
		uid(3): res(uid(3), 5, model.CompletenessMissing),
		uid(4): res(uid(4), 5, model.CompletenessMissing),
	}
	s := Aggregate(questions, results)
	if len(s.DomainScores) != 2 {
		t.Fatalf("got %d domain scores, want 2", len(s.DomainScores))
	}
	byName := map[string]model.DomainScore{}
	for _, d := range s.DomainScores {
		byName[d.DomainName] = d
	}
	if helper.DomainBand(byName["Network Security"]) != model.BandLow {
		t.Errorf("Network Security band = %s, want Low", helper.DomainBand(byName["Network Security"]))
	}
	if helper.DomainBand(byName["AI Security"]) != model.BandCritical {
		t.Errorf("AI Security band = %s, want Critical", helper.DomainBand(byName["AI Security"]))
	}
	if byName["AI Security"].IncompleteCount != 2 {
		t.Errorf("AI Security incomplete = %d, want 2", byName["AI Security"].IncompleteCount)
	}
}

func TestAggregateCountsFinalizationProgress(t *testing.T) {
	questions := []*model.Question{
		q(uid(1), uid(1001), "Data Security", model.ReviewFinalized),
		q(uid(2), uid(1001), "Data Security", model.ReviewFinalized),
		q(uid(3), uid(1001), "Data Security", model.ReviewAIDrafted),
		q(uid(4), uid(1001), "Data Security", model.ReviewPending),
	}
	s := Aggregate(questions, map[uuid.UUID]*model.ReviewResult{})
	if s.FinalizedCount != 2 {
		t.Errorf("FinalizedCount = %d, want 2", s.FinalizedCount)
	}
	if s.PendingFinalization != 1 {
		t.Errorf("PendingFinalization = %d, want 1 (only ai_drafted awaits sign-off)", s.PendingFinalization)
	}
	if s.QuestionCount != 4 {
		t.Errorf("QuestionCount = %d, want 4", s.QuestionCount)
	}
	if got := helper.PercentFinalized(s); got != 50 {
		t.Errorf("PercentFinalized = %d, want 50", got)
	}
}

func TestAggregateUnscoredQuestionsDoNotCount(t *testing.T) {
	questions := []*model.Question{
		q(uid(1), uid(1001), "Cloud Security", model.ReviewAIDrafted),
		q(uid(2), uid(1001), "Cloud Security", model.ReviewPending),
	}
	results := map[uuid.UUID]*model.ReviewResult{uid(1): res(uid(1), 4, model.CompletenessComplete)}
	s := Aggregate(questions, results)
	if s.ScoredCount != 1 {
		t.Errorf("ScoredCount = %d, want 1", s.ScoredCount)
	}
	if s.QuestionCount != 2 {
		t.Errorf("QuestionCount = %d, want 2", s.QuestionCount)
	}
	// An unreviewed question must not be scored as if it were a zero.
	if s.OverallScore != 4 {
		t.Errorf("OverallScore = %.2f, want 4.00", s.OverallScore)
	}
}

func TestAggregateEmpty(t *testing.T) {
	s := Aggregate(nil, nil)
	if s.QuestionCount != 0 || s.OverallScore != 0 {
		t.Errorf("empty aggregate = %+v", s)
	}
	if helper.SummaryBand(s) != model.BandUnknown {
		t.Errorf("empty band = %s, want unknown", helper.SummaryBand(s))
	}
}

func TestTopFindingsOrdersByRisk(t *testing.T) {
	questions := []*model.Question{
		q(uid(1), uid(1001), "Network Security", model.ReviewAIDrafted),
		q(uid(2), uid(1001), "Network Security", model.ReviewAIDrafted),
		q(uid(3), uid(1001), "Network Security", model.ReviewAIDrafted),
	}
	questions[0].QuestionText = "low"
	questions[1].QuestionText = "critical"
	questions[2].QuestionText = "medium"
	results := map[uuid.UUID]*model.ReviewResult{
		uid(1): res(uid(1), 1, model.CompletenessComplete),
		uid(2): res(uid(2), 5, model.CompletenessMissing, model.Flag{Kind: model.FlagMissingAnswer}),
		uid(3): res(uid(3), 3, model.CompletenessPartial),
	}
	top := TopFindings(questions, results, 2)
	if len(top) != 2 {
		t.Fatalf("got %d findings, want 2", len(top))
	}
	if top[0].Question != "critical" {
		t.Errorf("first finding = %q, want the critical one", top[0].Question)
	}
	if top[1].Question != "medium" {
		t.Errorf("second finding = %q, want the medium one", top[1].Question)
	}
	if len(top[0].Flags) != 1 {
		t.Errorf("flags on top finding = %d, want 1", len(top[0].Flags))
	}
}
