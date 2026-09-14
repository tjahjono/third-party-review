package assessment

import (
	"context"
	"strings"
	"time"

	"third-party-review/internal/domain"
)

// This file holds the human-in-the-loop half of the workflow: everything that
// turns an AI draft into feedback a person has signed their name to.
//
// The invariant every method here preserves: assessor_feedback_draft is what
// the AI wrote and is never modified by a human action. assessor_feedback_final
// is what a person signed off. Both are kept for the life of the assessment.

// GetQuestion returns one question with its latest AI result attached, for the
// inline editor.
func (s *Service) GetQuestion(ctx context.Context, questionID int64) (*domain.Question, error) {
	q, err := s.repos.Questions.GetByID(ctx, questionID)
	if err != nil {
		return nil, err
	}
	results, err := s.repos.Results.LatestByAssessment(ctx, q.AssessmentID, nil)
	if err != nil {
		return nil, err
	}
	if r, ok := results[q.ID]; ok {
		q.LatestResult = r
	}
	return q, nil
}

// FinalizeQuestion records the human-signed-off feedback for one question. The
// text passed in is whatever the reviewer had in the editor: the AI draft
// unchanged, or their edit of it.
func (s *Service) FinalizeQuestion(ctx context.Context, questionID int64, feedback string, userID *int64) (*domain.Question, error) {
	feedback = strings.TrimSpace(feedback)
	if feedback == "" {
		return nil, domain.ValidationError{
			Field:   "feedback",
			Message: "Feedback can't be empty. Write what the vendor needs to address, or reopen the question instead.",
		}
	}

	q, err := s.repos.Questions.GetByID(ctx, questionID)
	if err != nil {
		return nil, err
	}
	if err := s.assessmentIsOpen(ctx, q.AssessmentID); err != nil {
		return nil, err
	}

	if err := s.repos.Questions.Finalize(ctx, questionID, feedback, userID, time.Now().UTC()); err != nil {
		return nil, err
	}
	return s.GetQuestion(ctx, questionID)
}

// ReopenQuestion takes a question back out of the finalized state so it can be
// edited again. The signed-off text is kept, so an accidental reopen costs
// nothing.
func (s *Service) ReopenQuestion(ctx context.Context, questionID int64) (*domain.Question, error) {
	q, err := s.repos.Questions.GetByID(ctx, questionID)
	if err != nil {
		return nil, err
	}
	if err := s.assessmentIsOpen(ctx, q.AssessmentID); err != nil {
		return nil, err
	}
	if err := s.repos.Questions.Unfinalize(ctx, questionID); err != nil {
		return nil, err
	}
	return s.GetQuestion(ctx, questionID)
}

// BulkFinalizeResult reports what a bulk sign-off actually did.
type BulkFinalizeResult struct {
	Finalized int
	// Skipped counts questions passed over because they had no draft to sign
	// off. Accepting a blank draft would put an empty finding into the record.
	Skipped int
	// AlreadyFinal counts questions a human had already signed.
	AlreadyFinal int
}

// BulkFinalize signs off every not-yet-finalized question in an assessment, or
// in one domain of it, using each question's AI draft verbatim.
//
// This is the "accept the rest" action a reviewer reaches for after reading
// through a domain. It deliberately only accepts drafts as written - anything
// a reviewer wants to change still goes through the inline editor - and it
// refuses to sign off a question the AI left without a draft.
func (s *Service) BulkFinalize(ctx context.Context, assessmentID int64, domainID *int64, userID *int64) (BulkFinalizeResult, error) {
	var out BulkFinalizeResult

	if err := s.assessmentIsOpen(ctx, assessmentID); err != nil {
		return out, err
	}

	questions, err := s.repos.Questions.List(ctx, domain.QuestionFilter{
		AssessmentID: assessmentID,
		DomainID:     domainID,
	})
	if err != nil {
		return out, err
	}

	now := time.Now().UTC()
	err = s.repos.Tx.RunInTx(ctx, func(ctx context.Context) error {
		for _, q := range questions {
			if q.ReviewStatus == domain.ReviewFinalized {
				out.AlreadyFinal++
				continue
			}
			draft := strings.TrimSpace(q.AssessorFeedbackDraft)
			if draft == "" {
				out.Skipped++
				continue
			}
			if err := s.repos.Questions.Finalize(ctx, q.ID, draft, userID, now); err != nil {
				return err
			}
			out.Finalized++
		}
		return nil
	})
	if err != nil {
		return BulkFinalizeResult{}, err
	}

	s.log.Info("bulk sign-off",
		"assessment_id", assessmentID, "domain_id", domainID,
		"finalized", out.Finalized, "skipped", out.Skipped)
	return out, nil
}

// CloseAssessment marks an assessment complete. It refuses while questions are
// still unsigned: closing is the point at which the assessment becomes the
// record, and a record with unreviewed AI drafts in it is not one anybody
// should be relying on.
func (s *Service) CloseAssessment(ctx context.Context, assessmentID int64) error {
	a, err := s.repos.Assessments.GetByID(ctx, assessmentID)
	if err != nil {
		return err
	}
	if a.Status == domain.StatusClosed {
		return nil
	}
	if a.Status == domain.StatusReviewing {
		return domain.ValidationError{
			Field:   "status",
			Message: "An AI review is still running. Wait for it to finish before closing.",
		}
	}

	pending, err := s.countUnfinalized(ctx, assessmentID)
	if err != nil {
		return err
	}
	if pending > 0 {
		return domain.ValidationError{
			Field: "status",
			Message: plural(pending,
				"1 question still needs your sign-off before this assessment can be closed.",
				"%d questions still need your sign-off before this assessment can be closed."),
		}
	}

	// The original upload has served its purpose once the assessment is
	// closed; the confirmed mapping and the questions remain as the record.
	if err := s.repos.Assessments.DiscardUpload(ctx, assessmentID); err != nil {
		s.log.Warn("could not discard the stored upload", "assessment_id", assessmentID, "error", err)
	}
	return s.repos.Assessments.SetStatus(ctx, assessmentID, domain.StatusClosed, time.Now().UTC())
}

// ReopenAssessment moves a closed assessment back to reviewed so further work
// can be done on it.
func (s *Service) ReopenAssessment(ctx context.Context, assessmentID int64) error {
	a, err := s.repos.Assessments.GetByID(ctx, assessmentID)
	if err != nil {
		return err
	}
	if a.Status != domain.StatusClosed {
		return domain.ValidationError{Field: "status", Message: "That assessment is not closed."}
	}
	target := domain.StatusReviewed
	if a.CurrentRunID == nil {
		target = domain.StatusMapped
	}
	return s.repos.Assessments.SetStatus(ctx, assessmentID, target, time.Now().UTC())
}

// assessmentIsOpen refuses edits to a closed assessment. A closed assessment is
// the signed record; changing it silently would undermine the point of having
// signed it.
func (s *Service) assessmentIsOpen(ctx context.Context, assessmentID int64) error {
	a, err := s.repos.Assessments.GetByID(ctx, assessmentID)
	if err != nil {
		return err
	}
	if a.Status == domain.StatusClosed {
		return domain.ValidationError{
			Field:   "status",
			Message: "This assessment is closed. Reopen it before changing any feedback.",
		}
	}
	return nil
}

// countUnfinalized counts questions still awaiting human sign-off.
func (s *Service) countUnfinalized(ctx context.Context, assessmentID int64) (int, error) {
	questions, err := s.repos.Questions.List(ctx, domain.QuestionFilter{AssessmentID: assessmentID})
	if err != nil {
		return 0, err
	}
	n := 0
	for _, q := range questions {
		if q.ReviewStatus != domain.ReviewFinalized {
			n++
		}
	}
	return n, nil
}

// SignOffProgress is the reviewer-facing state of an assessment's sign-off.
type SignOffProgress struct {
	Total        int
	Finalized    int
	Pending      int
	NoDraft      int
	Percent      int
	ReadyToClose bool
}

// Progress reports how far the human review has got, for the header bar.
func (s *Service) Progress(ctx context.Context, assessmentID int64) (SignOffProgress, error) {
	questions, err := s.repos.Questions.List(ctx, domain.QuestionFilter{AssessmentID: assessmentID})
	if err != nil {
		return SignOffProgress{}, err
	}
	var p SignOffProgress
	p.Total = len(questions)
	for _, q := range questions {
		switch {
		case q.ReviewStatus == domain.ReviewFinalized:
			p.Finalized++
		case strings.TrimSpace(q.AssessorFeedbackDraft) == "":
			p.NoDraft++
			p.Pending++
		default:
			p.Pending++
		}
	}
	if p.Total > 0 {
		p.Percent = p.Finalized * 100 / p.Total
	}
	p.ReadyToClose = p.Total > 0 && p.Pending == 0
	return p, nil
}

// plural picks the singular or plural message and formats the count into it.
func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return strings.Replace(many, "%d", itoa(n), 1)
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [20]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(b[pos:])
}
