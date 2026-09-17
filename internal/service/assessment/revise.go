package assessment

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"third-party-review/internal/dto"
	"third-party-review/internal/helper"
	"third-party-review/internal/model"
	"third-party-review/pkg/fuzzy"

	"github.com/google/uuid"
)

// This file is the second half of a review cycle: the reviewed questionnaire
// went back to the vendor with assessor feedback, the vendor revised some of
// their answers, and the assessor uploads the updated file. Only what
// actually changed is meant to move - re-running the AI review or asking the
// vendor to reformat their whole submission would be overkill for what is
// usually a handful of updated rows.
//
// There is no stable per-question identifier that survives an export and a
// later re-upload (see export.go: the exported file carries only the
// template's 7 columns), so a re-uploaded row is matched back to an existing
// question by domain plus a fuzzy match on the question text, using the same
// pkg/fuzzy toolkit already used for header and domain-divider matching.

// answerMatchThreshold is how similar a re-uploaded row's question text has
// to be to an existing question's before the two are treated as the same
// question. Set high on purpose: a false match would silently overwrite the
// wrong question's answer, which is worse than leaving a row unmatched for
// the assessor to reconcile by hand.
const answerMatchThreshold = 0.90

// PreviewAnswerRevisions parses a re-uploaded questionnaire and matches its
// rows back to this assessment's existing questions. It writes nothing: the
// caller shows the result and collects which changes to apply.
func (s *Service) PreviewAnswerRevisions(ctx context.Context, assessmentID uuid.UUID, filename string, r io.Reader) (*dto.AnswerRevisionPreview, error) {
	a, err := s.assessments.GetByID(ctx, assessmentID)
	if err != nil {
		return nil, err
	}
	if a.Status == model.StatusReviewing {
		return nil, helper.ValidationError{
			Field:   "status",
			Message: "An AI review is running for this assessment. Wait for it to finish before uploading revised answers.",
		}
	}

	content, err := io.ReadAll(io.LimitReader(r, maxUploadBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read revised-answers upload: %w", err)
	}
	if len(content) == 0 {
		return nil, helper.ValidationError{Field: "file", Message: "The uploaded file is empty."}
	}
	if len(content) > maxUploadBytes {
		return nil, helper.ValidationError{Field: "file", Message: "That file is too large to process."}
	}

	domains, err := s.domains.List(ctx, false)
	if err != nil {
		return nil, err
	}
	parsed, err := s.parser.Parse(bytes.NewReader(content), filename, domains)
	if err != nil {
		return nil, err
	}
	if parsed.QuestionCount == 0 {
		return nil, helper.ValidationError{Field: "file", Message: "No question rows were found in that file."}
	}

	existing, err := s.questions.List(ctx, dto.QuestionFilter{AssessmentID: assessmentID})
	if err != nil {
		return nil, err
	}
	byDomain := make(map[uuid.UUID][]*model.Question, len(domains))
	for _, q := range existing {
		byDomain[q.DomainID] = append(byDomain[q.DomainID], q)
	}

	out := &dto.AnswerRevisionPreview{}
	claimed := make(map[uuid.UUID]bool, len(existing))

	for _, row := range parsed.Rows {
		if row.Kind != dto.RowQuestion || !row.HasDomain() {
			continue
		}
		text := strings.TrimSpace(row.QuestionText())
		if text == "" {
			continue
		}

		idx, score := bestUnclaimedMatch(text, byDomain[*row.DomainID], claimed)
		if idx < 0 || score < answerMatchThreshold {
			out.Unmatched = append(out.Unmatched, dto.UnmatchedRevisionRow{
				QuestionText: text, SourceRow: row.Index, BestScore: score,
			})
			continue
		}
		match := byDomain[*row.DomainID][idx]
		claimed[match.ID] = true

		newAnswer := strings.TrimSpace(row.Answer())
		oldAnswer := strings.TrimSpace(match.ThirdPartyAnswer)

		rev := dto.AnswerRevisionRow{
			QuestionID:   match.ID,
			QuestionText: match.QuestionText,
			DomainName:   match.DomainName,
			OldAnswer:    oldAnswer,
			NewAnswer:    newAnswer,
			// An empty re-uploaded answer is not treated as "the vendor
			// cleared their answer" - it almost always means the row didn't
			// carry an answer in that column, and silently wiping a recorded
			// answer would be a worse failure mode than ignoring the row.
			Changed:    newAnswer != "" && newAnswer != oldAnswer,
			MatchScore: score,
			SourceRow:  row.Index,
		}
		out.Rows = append(out.Rows, rev)
		if rev.Changed {
			out.ChangedCount++
		}
	}

	// Changed rows first, so the confirmation screen leads with what actually
	// needs a decision; unchanged matches and their source order are kept
	// after that as a secondary sort key.
	sort.SliceStable(out.Rows, func(i, j int) bool {
		if out.Rows[i].Changed != out.Rows[j].Changed {
			return out.Rows[i].Changed
		}
		return out.Rows[i].SourceRow < out.Rows[j].SourceRow
	})

	return out, nil
}

// ApplyAnswerRevisions writes the confirmed answer changes. Each revision
// resets its question to pending (see QuestionRepository.ReviseAnswer) so it
// goes through human sign-off again; the AI's previous draft and any previous
// final feedback are left in place as history.
//
// If applying anything leaves the assessment closed, it is reopened -
// following the same target-status rule as ReopenAssessment - so the revised
// answers land somewhere a reviewer can act on rather than back in a
// read-only record.
func (s *Service) ApplyAnswerRevisions(ctx context.Context, assessmentID uuid.UUID, revisions []dto.AnswerRevisionInput) (int, error) {
	a, err := s.assessments.GetByID(ctx, assessmentID)
	if err != nil {
		return 0, err
	}
	if a.Status == model.StatusReviewing {
		return 0, helper.ValidationError{
			Field:   "status",
			Message: "An AI review is running for this assessment. Wait for it to finish before applying revised answers.",
		}
	}
	if len(revisions) == 0 {
		return 0, nil
	}

	ids := make([]uuid.UUID, 0, len(revisions))
	for _, rev := range revisions {
		ids = append(ids, rev.QuestionID)
	}
	// Scoped to this assessment, same as every other bulk question lookup -
	// a stray id from elsewhere in the form can't touch another assessment's
	// question.
	existing, err := s.questions.ListByIDs(ctx, assessmentID, ids)
	if err != nil {
		return 0, err
	}
	byID := make(map[uuid.UUID]*model.Question, len(existing))
	for _, q := range existing {
		byID[q.ID] = q
	}

	applied := 0
	err = s.tx.RunInTx(ctx, func(ctx context.Context) error {
		for _, rev := range revisions {
			q, ok := byID[rev.QuestionID]
			if !ok {
				continue
			}
			newAnswer := strings.TrimSpace(rev.NewAnswer)
			if newAnswer == "" || newAnswer == strings.TrimSpace(q.ThirdPartyAnswer) {
				continue
			}
			if err := s.questions.ReviseAnswer(ctx, q.ID, newAnswer); err != nil {
				return err
			}
			applied++
		}
		if applied == 0 || a.Status != model.StatusClosed {
			return nil
		}
		target := model.StatusReviewed
		if a.CurrentRunID == nil {
			target = model.StatusMapped
		}
		return s.assessments.SetStatus(ctx, assessmentID, target, time.Now().UTC())
	})
	if err != nil {
		return 0, err
	}

	if applied > 0 {
		s.log.Info("third-party answers revised",
			"assessment_id", assessmentID, "questions_changed", applied)
	}
	return applied, nil
}

// bestUnclaimedMatch returns the index into candidates whose QuestionText is
// most similar to text, skipping any candidate already claimed by an earlier
// row - so two re-uploaded rows can never both be matched to the same
// existing question. Returns (-1, 0) when candidates is empty or every
// candidate is already claimed.
func bestUnclaimedMatch(text string, candidates []*model.Question, claimed map[uuid.UUID]bool) (int, float64) {
	best, bestScore := -1, 0.0
	for i, q := range candidates {
		if claimed[q.ID] {
			continue
		}
		score := fuzzy.Ratio(text, q.QuestionText)
		if score > bestScore {
			best, bestScore = i, score
		}
	}
	return best, bestScore
}
