package review

import (
	"math"
	"testing"

	"third-party-review/internal/domain"
)

func q(id, domainID int64, name string, status domain.ReviewStatus) *domain.Question {
	return &domain.Question{ID: id, DomainID: domainID, DomainName: name, ReviewStatus: status}
}

func res(qid int64, score domain.RiskScore, c domain.Completeness, flags ...domain.Flag) *domain.ReviewResult {
	return &domain.ReviewResult{QuestionID: qid, RiskScore: score, Completeness: c, Flags: flags}
}

// The behaviour this whole scheme exists for: a handful of serious findings
// among many good answers must not average away into "low risk".
func TestWeightedAggregateDoesNotDiluteCriticalFindings(t *testing.T) {
	var questions []*domain.Question
	results := map[int64]*domain.ReviewResult{}

	// 17 strong answers.
	for i := int64(1); i <= 17; i++ {
		questions = append(questions, q(i, 1, "Network Security", domain.ReviewAIDrafted))
		results[i] = res(i, 1, domain.CompletenessComplete)
	}
	// 3 serious gaps.
	for i := int64(18); i <= 20; i++ {
		questions = append(questions, q(i, 1, "Network Security", domain.ReviewAIDrafted))
		results[i] = res(i, 5, domain.CompletenessMissing,
			domain.Flag{Kind: domain.FlagMissingAnswer, Detail: "unanswered"})
	}

	s := Aggregate(questions, results)

	if math.Abs(s.MeanScore-1.6) > 0.01 {
		t.Errorf("MeanScore = %.2f, want 1.60", s.MeanScore)
	}
	// The plain mean would band this Low, which is the failure mode.
	if domain.BandFromFloat(s.MeanScore) != domain.BandLow {
		t.Fatalf("precondition: the plain mean should band Low, got %s", domain.BandFromFloat(s.MeanScore))
	}
	if s.OverallScore <= s.MeanScore {
		t.Errorf("OverallScore (%.2f) must exceed the plain mean (%.2f)", s.OverallScore, s.MeanScore)
	}
	if got := domain.BandFromFloat(s.OverallScore); got == domain.BandLow {
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
	var questions []*domain.Question
	results := map[int64]*domain.ReviewResult{}
	for i := int64(1); i <= 10; i++ {
		questions = append(questions, q(i, 1, "Data Security", domain.ReviewAIDrafted))
		results[i] = res(i, 3, domain.CompletenessComplete)
	}
	s := Aggregate(questions, results)
	// With no spread the weighted score must equal the mean exactly.
	if s.OverallScore != 3 || s.MeanScore != 3 {
		t.Errorf("uniform scores: overall = %.2f, mean = %.2f, want 3.00 for both", s.OverallScore, s.MeanScore)
	}
	if s.DomainScores[0].Band() != domain.BandMedium {
		t.Errorf("band = %s, want Medium", s.DomainScores[0].Band())
	}
}

func TestAggregatePerDomainSeparation(t *testing.T) {
	questions := []*domain.Question{
		q(1, 1, "Network Security", domain.ReviewAIDrafted),
		q(2, 1, "Network Security", domain.ReviewAIDrafted),
		q(3, 2, "AI Security", domain.ReviewAIDrafted),
		q(4, 2, "AI Security", domain.ReviewAIDrafted),
	}
	results := map[int64]*domain.ReviewResult{
		1: res(1, 1, domain.CompletenessComplete),
		2: res(2, 2, domain.CompletenessComplete),
		3: res(3, 5, domain.CompletenessMissing),
		4: res(4, 5, domain.CompletenessMissing),
	}
	s := Aggregate(questions, results)
	if len(s.DomainScores) != 2 {
		t.Fatalf("got %d domain scores, want 2", len(s.DomainScores))
	}
	byName := map[string]domain.DomainScore{}
	for _, d := range s.DomainScores {
		byName[d.DomainName] = d
	}
	if byName["Network Security"].Band() != domain.BandLow {
		t.Errorf("Network Security band = %s, want Low", byName["Network Security"].Band())
	}
	if byName["AI Security"].Band() != domain.BandCritical {
		t.Errorf("AI Security band = %s, want Critical", byName["AI Security"].Band())
	}
	if byName["AI Security"].IncompleteCount != 2 {
		t.Errorf("AI Security incomplete = %d, want 2", byName["AI Security"].IncompleteCount)
	}
}

func TestAggregateCountsFinalizationProgress(t *testing.T) {
	questions := []*domain.Question{
		q(1, 1, "Data Security", domain.ReviewFinalized),
		q(2, 1, "Data Security", domain.ReviewFinalized),
		q(3, 1, "Data Security", domain.ReviewAIDrafted),
		q(4, 1, "Data Security", domain.ReviewPending),
	}
	s := Aggregate(questions, map[int64]*domain.ReviewResult{})
	if s.FinalizedCount != 2 {
		t.Errorf("FinalizedCount = %d, want 2", s.FinalizedCount)
	}
	if s.PendingFinalization != 1 {
		t.Errorf("PendingFinalization = %d, want 1 (only ai_drafted awaits sign-off)", s.PendingFinalization)
	}
	if s.QuestionCount != 4 {
		t.Errorf("QuestionCount = %d, want 4", s.QuestionCount)
	}
	if got := s.PercentFinalized(); got != 50 {
		t.Errorf("PercentFinalized = %d, want 50", got)
	}
}

func TestAggregateUnscoredQuestionsDoNotCount(t *testing.T) {
	questions := []*domain.Question{
		q(1, 1, "Cloud Security", domain.ReviewAIDrafted),
		q(2, 1, "Cloud Security", domain.ReviewPending),
	}
	results := map[int64]*domain.ReviewResult{1: res(1, 4, domain.CompletenessComplete)}
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
	if s.Band() != domain.BandUnknown {
		t.Errorf("empty band = %s, want unknown", s.Band())
	}
}

func TestTopFindingsOrdersByRisk(t *testing.T) {
	questions := []*domain.Question{
		q(1, 1, "Network Security", domain.ReviewAIDrafted),
		q(2, 1, "Network Security", domain.ReviewAIDrafted),
		q(3, 1, "Network Security", domain.ReviewAIDrafted),
	}
	questions[0].QuestionText = "low"
	questions[1].QuestionText = "critical"
	questions[2].QuestionText = "medium"
	results := map[int64]*domain.ReviewResult{
		1: res(1, 1, domain.CompletenessComplete),
		2: res(2, 5, domain.CompletenessMissing, domain.Flag{Kind: domain.FlagMissingAnswer}),
		3: res(3, 3, domain.CompletenessPartial),
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
