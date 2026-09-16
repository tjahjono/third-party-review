package dashboard

import (
	"context"
	"log/slog"
	"sort"

	"third-party-review/internal/dto"
	"third-party-review/internal/helper"
	"third-party-review/internal/model"
	"third-party-review/internal/repository"
)

// scanLimit bounds the single broad assessment fetch the dashboard builds
// everything else from (status counts, risk bands, needs-attention). This is
// a single internal team's workload, not a high-volume product, so one query
// well above any realistic assessment count is simpler and cheaper than a
// handful of separate aggregate queries - see the project's own framing of
// scale in section 6 of the brief.
const scanLimit = 2000

// recentLimit is how many of the newest assessments the home page lists.
const recentLimit = 8

// attentionLimit is how many of the highest-risk assessments the home page
// calls out.
const attentionLimit = 5

type Service struct {
	vendors     repository.VendorRepository
	assessments repository.AssessmentRepository
	jobs        repository.ReviewJobRepository
	log         *slog.Logger
}

func New(deps Deps) (*Service, error) {
	if err := deps.validate(); err != nil {
		return nil, err
	}
	return &Service{
		vendors:     deps.Vendors,
		assessments: deps.Assessments,
		jobs:        deps.Jobs,
		log:         deps.Log,
	}, nil
}

// MustNew is New for wiring that cannot meaningfully recover.
func MustNew(deps Deps) *Service {
	s, err := New(deps)
	if err != nil {
		panic(err)
	}
	return s
}

// Build assembles the home-page dashboard from a handful of existing
// repository queries: one broad assessment listing supplies status counts,
// risk-band distribution, the recent list and the needs-attention list, so
// none of the four analyses can disagree with each other over a data race
// between separate queries.
func (s *Service) Build(ctx context.Context) (*dto.Dashboard, error) {
	vendorCount, err := s.vendors.Count(ctx, "")
	if err != nil {
		return nil, err
	}
	assessmentCount, err := s.assessments.Count(ctx, dto.AssessmentFilter{})
	if err != nil {
		return nil, err
	}
	all, err := s.assessments.List(ctx, dto.AssessmentFilter{Limit: scanLimit})
	if err != nil {
		return nil, err
	}
	active, err := s.jobs.ListActive(ctx)
	if err != nil {
		return nil, err
	}

	d := &dto.Dashboard{
		VendorCount:     vendorCount,
		AssessmentCount: assessmentCount,
		StatusCounts:    map[model.AssessmentStatus]int{},
		RiskBandCounts:  map[model.RiskBand]int{},
		ActiveReviews:   active,
	}
	for _, status := range model.AllAssessmentStatuses {
		d.StatusCounts[status] = 0
	}

	var reviewed []*model.Assessment
	for _, a := range all {
		d.StatusCounts[a.Status]++
		if a.Summary == nil {
			continue
		}
		d.ReviewedCount++
		d.RiskBandCounts[helper.SummaryBand(a.Summary)]++
		d.PendingFinalization += a.Summary.PendingFinalization
		reviewed = append(reviewed, a)
	}

	// List is already ordered newest-first, so the recent list is just its
	// head - no separate query or re-sort needed.
	if len(all) > recentLimit {
		d.RecentAssessments = all[:recentLimit]
	} else {
		d.RecentAssessments = all
	}

	needsAttention := append([]*model.Assessment(nil), reviewed...)
	sort.SliceStable(needsAttention, func(i, j int) bool {
		si, sj := needsAttention[i].Summary, needsAttention[j].Summary
		if si.FlaggedCount != sj.FlaggedCount {
			return si.FlaggedCount > sj.FlaggedCount
		}
		return si.OverallScore > sj.OverallScore
	})
	if len(needsAttention) > attentionLimit {
		needsAttention = needsAttention[:attentionLimit]
	}
	d.NeedsAttention = needsAttention

	return d, nil
}
