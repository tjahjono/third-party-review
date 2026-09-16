// Package review orchestrates AI review of an assessment: batching questions,
// calling the configured provider, persisting results, aggregating scores and
// running the whole thing as a background job.
package review

import (
	"math"
	"sort"

	"third-party-review/internal/dto"
	"third-party-review/internal/helper"
	"third-party-review/internal/model"

	"github.com/google/uuid"
)

// worstCaseWeight controls how much the worst answers pull the aggregate away
// from the plain mean.
//
// A straight average is the wrong summary for a security assessment: a vendor
// with sixty solid answers and three critical gaps averages out to "low risk",
// which is precisely the conclusion the assessment exists to prevent. The
// aggregate is therefore a blend of the mean and the mean of the worst
// quintile, weighted towards the latter.
const (
	worstCaseWeight = 0.6
	worstQuintile   = 0.2
	minWorstSampleN = 1
)

// Aggregate computes per-domain and assessment-level scores from the latest
// result per question. Scoring lives in Go rather than in the model so the
// numbers are deterministic, explainable and identical across providers.
func Aggregate(questions []*model.Question, results map[uuid.UUID]*model.ReviewResult) *model.AssessmentSummary {
	summary := &model.AssessmentSummary{QuestionCount: len(questions)}

	type bucket struct {
		name       string
		sortOrder  int
		scores     []model.RiskScore
		flagged    int
		incomplete int
		total      int
	}
	buckets := map[uuid.UUID]*bucket{}

	var allScores []model.RiskScore

	for _, q := range questions {
		b, ok := buckets[q.DomainID]
		if !ok {
			b = &bucket{name: q.DomainName}
			buckets[q.DomainID] = b
		}
		b.total++

		switch q.ReviewStatus {
		case model.ReviewFinalized:
			summary.FinalizedCount++
		case model.ReviewAIDrafted:
			summary.PendingFinalization++
		}

		r := results[q.ID]
		if r == nil {
			continue
		}
		if helper.ValidRiskScore(r.RiskScore) {
			b.scores = append(b.scores, r.RiskScore)
			allScores = append(allScores, r.RiskScore)
			summary.ScoredCount++
			if r.RiskScore > summary.WorstScore {
				summary.WorstScore = r.RiskScore
			}
		}
		if len(r.Flags) > 0 || r.RiskScore >= model.RiskFlagThreshold {
			b.flagged++
			summary.FlaggedCount++
		}
		if helper.Incomplete(r.Completeness) {
			b.incomplete++
			summary.IncompleteCount++
		}
	}

	// Preserve the seeded domain ordering where it is known; questions carry
	// it implicitly through the order the repository returns them in.
	order := 0
	seen := map[uuid.UUID]bool{}
	for _, q := range questions {
		if !seen[q.DomainID] {
			seen[q.DomainID] = true
			if b := buckets[q.DomainID]; b != nil {
				b.sortOrder = order
			}
			order++
		}
	}

	for id, b := range buckets {
		ds := model.DomainScore{
			DomainID:        id,
			DomainName:      b.name,
			SortOrder:       b.sortOrder,
			QuestionCount:   b.total,
			ScoredCount:     len(b.scores),
			FlaggedCount:    b.flagged,
			IncompleteCount: b.incomplete,
		}
		ds.MeanScore, ds.WorstScore, ds.WeightedScore = blend(b.scores)
		summary.DomainScores = append(summary.DomainScores, ds)
	}
	helper.SortDomainScores(summary.DomainScores)

	summary.MeanScore, _, summary.OverallScore = blend(allScores)
	return summary
}

// blend returns the mean, the worst score, and the weighted worst-case
// aggregate for a set of scores.
func blend(scores []model.RiskScore) (mean float64, worst model.RiskScore, weighted float64) {
	if len(scores) == 0 {
		return 0, 0, 0
	}
	sum := 0
	for _, s := range scores {
		sum += int(s)
		if s > worst {
			worst = s
		}
	}
	mean = float64(sum) / float64(len(scores))

	// Mean of the worst quintile (at least one answer).
	sorted := make([]model.RiskScore, len(scores))
	copy(sorted, scores)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] > sorted[j] })

	n := int(math.Ceil(float64(len(sorted)) * worstQuintile))
	if n < minWorstSampleN {
		n = minWorstSampleN
	}
	if n > len(sorted) {
		n = len(sorted)
	}
	worstSum := 0
	for _, s := range sorted[:n] {
		worstSum += int(s)
	}
	worstMean := float64(worstSum) / float64(n)

	weighted = (1-worstCaseWeight)*mean + worstCaseWeight*worstMean
	// Round to two decimals so stored and displayed values always agree.
	weighted = math.Round(weighted*100) / 100
	mean = math.Round(mean*100) / 100
	return mean, worst, weighted
}

// TopFindings returns the highest-risk results, newest scoring first, capped
// at limit. Used to keep the narrative request inside the context window.
func TopFindings(questions []*model.Question, results map[uuid.UUID]*model.ReviewResult, limit int) []dto.SummaryFinding {
	type scored struct {
		f     dto.SummaryFinding
		score model.RiskScore
		flags int
	}
	var all []scored
	for _, q := range questions {
		r := results[q.ID]
		if r == nil {
			continue
		}
		f := dto.SummaryFinding{
			Domain:       q.DomainName,
			Question:     q.QuestionText,
			RiskScore:    r.RiskScore,
			Completeness: r.Completeness,
		}
		for _, fl := range r.Flags {
			f.Flags = append(f.Flags, helper.FlagKindLabel(fl.Kind))
		}
		all = append(all, scored{f: f, score: r.RiskScore, flags: len(r.Flags)})
	}
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].score != all[j].score {
			return all[i].score > all[j].score
		}
		return all[i].flags > all[j].flags
	})
	if limit > 0 && len(all) > limit {
		all = all[:limit]
	}
	out := make([]dto.SummaryFinding, 0, len(all))
	for _, s := range all {
		out = append(out, s.f)
	}
	return out
}
