package review

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"third-party-review/internal/config"
	"third-party-review/internal/dto"
	"third-party-review/internal/helper"
	"third-party-review/internal/model"
	"third-party-review/internal/repository"
	"third-party-review/internal/service/aiclient"

	"github.com/google/uuid"
)

// lowConfidence is the threshold below which a batched result is re-checked
// with a single-question call. Batching buys cross-answer awareness; the
// follow-up pass buys back the attention a long batch costs on any one answer.
const lowConfidence = 0.35

// peerSampleSize caps how many other answers are shown as cross-domain context
// so a large questionnaire cannot blow the context window.
const peerSampleSize = 12

type Service struct {
	tx          helper.TxManager
	assessments repository.AssessmentRepository
	questions   repository.QuestionRepository
	results     repository.ReviewResultRepository
	summaries   repository.AssessmentSummaryRepository
	rubrics     repository.AssessmentRubricRepository
	jobs        repository.ReviewJobRepository
	reviewer    aiclient.AIReviewer
	cfg         config.AI
	log         *slog.Logger
}

func New(deps Deps) (*Service, error) {
	if err := deps.validate(); err != nil {
		return nil, err
	}
	return &Service{
		tx:          deps.Tx,
		assessments: deps.Assessments,
		questions:   deps.Questions,
		results:     deps.Results,
		summaries:   deps.Summaries,
		rubrics:     deps.Rubrics,
		jobs:        deps.Jobs,
		reviewer:    deps.Reviewer,
		cfg:         deps.Config,
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

// Enqueue validates that a review may start and creates a queued job. The
// actual work happens in the background worker, so the HTTP request returns
// immediately and the UI polls for progress.
func (s *Service) Enqueue(ctx context.Context, assessmentID uuid.UUID) (*model.ReviewJob, error) {
	a, err := s.assessments.GetByID(ctx, assessmentID)
	if err != nil {
		return nil, err
	}
	if !helper.CanStartReview(a.Status) {
		return nil, helper.ValidationError{
			Field: "status",
			Message: fmt.Sprintf("An assessment in %q cannot be reviewed. Map the questionnaire first.",
				helper.AssessmentStatusLabel(a.Status)),
		}
	}
	active, err := s.jobs.HasActive(ctx, assessmentID)
	if err != nil {
		return nil, err
	}
	if active {
		return nil, helper.ValidationError{
			Field:   "status",
			Message: "A review is already queued or running for this assessment.",
		}
	}
	count, err := s.questions.CountByAssessment(ctx, assessmentID)
	if err != nil {
		return nil, err
	}
	if count == 0 {
		return nil, helper.ValidationError{
			Field:   "questions",
			Message: "This assessment has no questions to review.",
		}
	}

	job := &model.ReviewJob{
		AssessmentID:   assessmentID,
		Status:         model.JobQueued,
		TotalQuestions: count,
		Stage:          "Queued",
		Provider:       s.reviewer.Name(),
		Model:          s.cfg.Model,
	}
	if rubric, err := s.rubrics.GetRubric(ctx, assessmentID); err == nil {
		job.RubricID = &rubric.ID
	} else if !errors.Is(err, helper.ErrNotFound) {
		return nil, err
	}

	if err := s.jobs.Create(ctx, job); err != nil {
		return nil, err
	}
	s.log.Info("review queued", "assessment_id", assessmentID, "job_id", job.ID, "questions", count)
	return job, nil
}

// Status returns the latest job for an assessment, for the HTMX progress poll.
func (s *Service) Status(ctx context.Context, assessmentID uuid.UUID) (*model.ReviewJob, error) {
	return s.jobs.LatestByAssessment(ctx, assessmentID)
}

// Finish records the terminal state of a run: the job row, and - when the run
// failed - the assessment returned to `mapped` so the user can retry instead
// of being stuck in `reviewing` forever.
//
// It is separate from Run because a panic inside Run has to be caught by the
// caller before the outcome can be recorded, and because the context that
// records the outcome must survive the cancellation that caused it. Every
// caller of Run must call Finish.
func (s *Service) Finish(ctx context.Context, job *model.ReviewJob, runErr error) error {
	if runErr == nil {
		return s.jobs.Finish(ctx, job.ID, model.JobSucceeded, "")
	}

	status := model.JobFailed
	if errors.Is(runErr, context.Canceled) {
		status = model.JobCancelled
	}
	if err := s.jobs.Finish(ctx, job.ID, status, runErr.Error()); err != nil {
		return err
	}
	if err := s.assessments.SetStatus(ctx, job.AssessmentID, model.StatusMapped, time.Now().UTC()); err != nil {
		return fmt.Errorf("reset assessment status after a failed review: %w", err)
	}
	return nil
}

// Run executes one review job to completion. It is called by the worker, and
// directly by tests. Progress is written to the job row as it goes so the UI
// poll has something real to show. Each chunk's results are persisted as soon
// as that chunk is reviewed, rather than held in memory for one write at the
// end - a large questionnaire takes minutes, and a reviewer (or a run that
// fails partway through) should see completed questions immediately instead
// of losing everything reviewed so far.
func (s *Service) Run(ctx context.Context, job *model.ReviewJob) error {
	started := time.Now()
	assessmentID := job.AssessmentID

	if err := s.assessments.SetStatus(ctx, assessmentID, model.StatusReviewing, time.Now().UTC()); err != nil {
		return err
	}
	// Set eagerly, not after the run completes: results start landing under
	// this run's id from the first persisted chunk on, and current_run_id is
	// purely informational (LatestByAssessment is not scoped by it), so there
	// is nothing to lose by pointing to it early.
	if err := s.assessments.SetCurrentRun(ctx, assessmentID, job.ID); err != nil {
		return err
	}

	assessment, err := s.assessments.GetByID(ctx, assessmentID)
	if err != nil {
		return err
	}
	questions, err := s.questions.ListForReview(ctx, assessmentID)
	if err != nil {
		return err
	}
	if len(questions) == 0 {
		return fmt.Errorf("assessment %d has no questions", assessmentID)
	}

	rubricExcerpt := ""
	if rubric, err := s.rubrics.GetRubric(ctx, assessmentID); err == nil {
		rubricExcerpt = helper.RubricExcerpt(rubric, maxRubricExcerpt)
	} else if !errors.Is(err, helper.ErrNotFound) {
		return err
	}

	peers := buildPeers(questions, peerSampleSize)
	groups := groupByDomain(questions)

	var (
		notes      []string
		done       int
		failed     int
		persisted  int
		groupIndex int
	)

	for _, g := range groups {
		groupIndex++
		stage := fmt.Sprintf("Reviewing %s (%d of %d)", g.domainName, groupIndex, len(groups))
		if err := s.jobs.UpdateProgress(ctx, job.ID, done, failed, stage); err != nil {
			s.log.Warn("progress update failed", "job_id", job.ID, "error", err)
		}

		for _, chunk := range chunkQuestions(g.questions, s.cfg.BatchSize) {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}

			batchResults, batchNotes, batchFailed := s.reviewChunk(ctx, assessment, chunk, rubricExcerpt, peers, job.ID)
			notes = append(notes, batchNotes...)
			done += len(batchResults)
			failed += batchFailed

			if len(batchResults) > 0 {
				if err := s.persistChunk(ctx, batchResults); err != nil {
					return fmt.Errorf("persist reviewed chunk: %w", err)
				}
				persisted += len(batchResults)
			}

			if err := s.jobs.UpdateProgress(ctx, job.ID, done, failed, stage); err != nil {
				s.log.Warn("progress update failed", "job_id", job.ID, "error", err)
			}
		}
	}

	if persisted == 0 {
		return fmt.Errorf("the AI provider returned no usable results for any of the %d questions", len(questions))
	}

	if err := s.buildSummary(ctx, assessment, job.ID, notes); err != nil {
		// A summary failure must not discard per-question work that succeeded.
		s.log.Error("summary generation failed; per-question results were kept",
			"assessment_id", assessmentID, "error", err)
	}

	if err := s.assessments.SetStatus(ctx, assessmentID, model.StatusReviewed, time.Now().UTC()); err != nil {
		return err
	}
	if err := s.jobs.UpdateProgress(ctx, job.ID, done, failed, "Completed"); err != nil {
		s.log.Warn("final progress update failed", "job_id", job.ID, "error", err)
	}

	s.log.Info("review complete",
		"assessment_id", assessmentID, "job_id", job.ID,
		"reviewed", persisted, "failed", failed, "duration", time.Since(started).Round(time.Second))
	return nil
}

// persistChunk writes one batch's results and drafts together, so a reviewer
// never sees a score without the feedback that explains it, and commits
// immediately rather than waiting for the rest of the assessment.
func (s *Service) persistChunk(ctx context.Context, results []*model.ReviewResult) error {
	return s.tx.RunInTx(ctx, func(ctx context.Context) error {
		if err := s.results.BulkCreate(ctx, results); err != nil {
			return err
		}
		for _, r := range results {
			if strings.TrimSpace(r.FeedbackDraft) == "" {
				continue
			}
			if err := s.questions.ApplyDraft(ctx, r.QuestionID, r.FeedbackDraft); err != nil {
				return err
			}
		}
		return nil
	})
}

const maxRubricExcerpt = 6000

// reviewChunk reviews one batch, falling back to per-question calls for
// anything the batch dropped or scored with low confidence.
func (s *Service) reviewChunk(
	ctx context.Context,
	assessment *model.Assessment,
	chunk []*model.Question,
	rubric string,
	peers []dto.PeerAnswer,
	runID uuid.UUID,
) ([]*model.ReviewResult, []string, int) {
	contexts := make([]dto.QuestionContext, 0, len(chunk))
	for _, q := range chunk {
		contexts = append(contexts, toContext(q))
	}

	var (
		out    []*model.ReviewResult
		notes  []string
		failed int
	)

	batch, err := s.reviewer.ReviewBatch(ctx, dto.BatchReviewRequest{
		AssessmentTitle: assessment.Title,
		VendorName:      assessment.VendorName,
		Questions:       contexts,
		RubricExcerpt:   rubric,
		PeerAnswers:     excludeSelf(peers, chunk),
	})
	if err != nil {
		s.log.Warn("batch review failed, falling back to per-question calls",
			"assessment_id", assessment.ID, "batch_size", len(chunk), "error", err)
	} else {
		notes = batch.Notes
	}

	for _, q := range chunk {
		r, ok := batch.Results[q.ID]
		needsFollowUp := !ok || r.Confidence < lowConfidence || strings.TrimSpace(r.FeedbackDraft) == ""

		if needsFollowUp {
			single, sErr := s.reviewer.ReviewAnswer(ctx, dto.ReviewRequest{
				Question:      toContext(q),
				RubricExcerpt: rubric,
				PeerAnswers:   excludeSelf(peers, chunk),
			})
			if sErr == nil {
				r, ok = single, true
			} else if !ok {
				s.log.Error("question review failed",
					"question_id", q.ID, "assessment_id", assessment.ID, "error", sErr)
				failed++
				continue
			}
		}

		r.QuestionID = q.ID
		r.RunID = runID
		if r.Provider == "" {
			r.Provider = s.reviewer.Name()
		}
		if r.Model == "" {
			r.Model = s.cfg.Model
		}
		helper.NormalizeResult(&r)
		rr := r
		out = append(out, &rr)
	}
	return out, notes, failed
}

// buildSummary aggregates the run and asks the provider for a narrative.
func (s *Service) buildSummary(ctx context.Context, assessment *model.Assessment, runID uuid.UUID, notes []string) error {
	questions, err := s.questions.ListForReview(ctx, assessment.ID)
	if err != nil {
		return err
	}
	results, err := s.results.LatestByAssessment(ctx, assessment.ID, &runID)
	if err != nil {
		return err
	}

	summary := Aggregate(questions, results)
	summary.AssessmentID = assessment.ID
	summary.RunID = &runID

	narrative, err := s.reviewer.Summarize(ctx, dto.SummaryRequest{
		VendorName:      assessment.VendorName,
		AssessmentTitle: assessment.Title,
		OverallScore:    summary.OverallScore,
		FlaggedCount:    summary.FlaggedCount,
		IncompleteCount: summary.IncompleteCount,
		QuestionCount:   summary.QuestionCount,
		DomainScores:    summary.DomainScores,
		TopFindings:     TopFindings(questions, results, 12),
		BatchNotes:      notes,
	})
	if err != nil {
		// Keep the computed figures even when the prose fails; they are the
		// part a reviewer cannot reconstruct by reading.
		s.log.Warn("narrative generation failed; storing computed summary only", "error", err)
	} else {
		summary.Narrative = narrative
	}
	return s.summaries.Upsert(ctx, summary)
}

// RecomputeSummary re-aggregates without calling the AI. Used after a human
// finalizes feedback so sign-off progress stays current.
func (s *Service) RecomputeSummary(ctx context.Context, assessmentID uuid.UUID) (*model.AssessmentSummary, error) {
	questions, err := s.questions.ListForReview(ctx, assessmentID)
	if err != nil {
		return nil, err
	}
	results, err := s.results.LatestByAssessment(ctx, assessmentID, nil)
	if err != nil {
		return nil, err
	}
	summary := Aggregate(questions, results)
	summary.AssessmentID = assessmentID

	if existing, err := s.summaries.GetByAssessment(ctx, assessmentID); err == nil {
		summary.Narrative = existing.Narrative
		summary.RunID = existing.RunID
	} else if !errors.Is(err, helper.ErrNotFound) {
		return nil, err
	}
	if err := s.summaries.Upsert(ctx, summary); err != nil {
		return nil, err
	}
	return summary, nil
}

type domainGroup struct {
	domainID   uuid.UUID
	domainName string
	questions  []*model.Question
}

// groupByDomain keeps each domain's questions together. Reviewing a domain as
// a unit is what lets the model notice that two answers within it contradict
// each other, and it keeps the per-domain scrutiny note relevant to every
// question in the call.
func groupByDomain(questions []*model.Question) []domainGroup {
	var groups []domainGroup
	index := map[uuid.UUID]int{}
	for _, q := range questions {
		i, ok := index[q.DomainID]
		if !ok {
			groups = append(groups, domainGroup{domainID: q.DomainID, domainName: q.DomainName})
			i = len(groups) - 1
			index[q.DomainID] = i
		}
		groups[i].questions = append(groups[i].questions, q)
	}
	return groups
}

// chunkQuestions splits a domain's questions into provider-sized batches.
func chunkQuestions(questions []*model.Question, size int) [][]*model.Question {
	if size < 1 {
		size = 1
	}
	var out [][]*model.Question
	for i := 0; i < len(questions); i += size {
		end := i + size
		if end > len(questions) {
			end = len(questions)
		}
		out = append(out, questions[i:end])
	}
	return out
}

// buildPeers samples answered questions from across the assessment so a batch
// scoped to one domain can still spot a contradiction with another.
func buildPeers(questions []*model.Question, limit int) []dto.PeerAnswer {
	var answered []*model.Question
	for _, q := range questions {
		if !helper.AnswerIsBlank(q) {
			answered = append(answered, q)
		}
	}
	if len(answered) == 0 {
		return nil
	}
	step := 1
	if len(answered) > limit && limit > 0 {
		step = len(answered) / limit
	}
	var peers []dto.PeerAnswer
	for i := 0; i < len(answered) && len(peers) < limit; i += step {
		q := answered[i]
		peers = append(peers, dto.PeerAnswer{
			QuestionID: q.ID,
			Domain:     q.DomainName,
			Question:   q.QuestionText,
			Answer:     q.ThirdPartyAnswer,
		})
	}
	return peers
}

// excludeSelf drops peers that are already in the current batch, so the model
// is not shown the same answer twice.
func excludeSelf(peers []dto.PeerAnswer, chunk []*model.Question) []dto.PeerAnswer {
	if len(peers) == 0 {
		return nil
	}
	inChunk := make(map[uuid.UUID]bool, len(chunk))
	for _, q := range chunk {
		inChunk[q.ID] = true
	}
	out := make([]dto.PeerAnswer, 0, len(peers))
	for _, p := range peers {
		if !inChunk[p.QuestionID] {
			out = append(out, p)
		}
	}
	return out
}

// toContext builds the neutral AI view of a question.
func toContext(q *model.Question) dto.QuestionContext {
	return dto.QuestionContext{
		QuestionID:       q.ID,
		QuestionText:     q.QuestionText,
		DomainName:       q.DomainName,
		ScrutinyNote:     q.ScrutinyNote,
		ThirdPartyAnswer: q.ThirdPartyAnswer,
		ThirdPartyRemark: q.ThirdPartyRemark,
		AssessorRemark:   q.AssessorRemark,
		HasEvidence:      helper.HasEvidence(q),
	}
}
