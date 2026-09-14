package integration

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"strings"
	"testing"

	"third-party-review/internal/domain"
)

// TestSignOffFlow walks the human half of the workflow: edit a draft, sign it,
// reopen it, and bulk-accept the rest.
func TestSignOffFlow(t *testing.T) {
	env := newTestEnv(t)
	env.truncateAll(t)
	ctx := context.Background()

	questions := mustIngestAndReview(t, env)
	assessmentID := questions[0].AssessmentID
	first := questions[0]
	originalDraft := first.AssessorFeedbackDraft

	// --- edit and sign one question ----------------------------------------
	edited := "Reviewed: the segmentation answer is adequate. Request the ACL review evidence next cycle."
	signed, err := env.Assess.FinalizeQuestion(ctx, first.ID, edited, nil)
	if err != nil {
		t.Fatalf("FinalizeQuestion: %v", err)
	}
	if signed.AssessorFeedbackFinal != edited {
		t.Errorf("final = %q, want the edited text", signed.AssessorFeedbackFinal)
	}
	if signed.AssessorFeedbackDraft != originalDraft {
		t.Error("signing off overwrote the AI draft")
	}
	if signed.ReviewStatus != domain.ReviewFinalized {
		t.Errorf("status = %s, want finalized", signed.ReviewStatus)
	}
	if signed.LatestResult == nil {
		t.Error("the AI result should still be attached after sign-off")
	}

	// Empty feedback is refused: a signed finding with no words in it is worse
	// than an unsigned one, because it reads as reviewed.
	if _, err := env.Assess.FinalizeQuestion(ctx, first.ID, "   ", nil); !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("blank feedback error = %v, want invalid input", err)
	}

	// --- progress reflects it ----------------------------------------------
	progress, err := env.Assess.Progress(ctx, assessmentID)
	if err != nil {
		t.Fatalf("Progress: %v", err)
	}
	if progress.Finalized != 1 {
		t.Errorf("Finalized = %d, want 1", progress.Finalized)
	}
	if progress.Pending != len(questions)-1 {
		t.Errorf("Pending = %d, want %d", progress.Pending, len(questions)-1)
	}
	if progress.ReadyToClose {
		t.Error("ReadyToClose should be false with questions outstanding")
	}

	// --- reopen keeps the human's words ------------------------------------
	reopened, err := env.Assess.ReopenQuestion(ctx, first.ID)
	if err != nil {
		t.Fatalf("ReopenQuestion: %v", err)
	}
	if reopened.ReviewStatus != domain.ReviewAIDrafted {
		t.Errorf("status after reopening = %s, want ai_drafted", reopened.ReviewStatus)
	}
	if reopened.AssessorFeedbackFinal != edited {
		t.Error("reopening discarded the human's edit")
	}

	// --- bulk accept the rest ----------------------------------------------
	res, err := env.Assess.BulkFinalize(ctx, assessmentID, nil, nil)
	if err != nil {
		t.Fatalf("BulkFinalize: %v", err)
	}
	if res.Finalized != len(questions) {
		t.Errorf("bulk signed %d, want %d (the reopened one included)", res.Finalized, len(questions))
	}

	progress, _ = env.Assess.Progress(ctx, assessmentID)
	if !progress.ReadyToClose || progress.Pending != 0 {
		t.Errorf("after bulk sign-off: pending = %d, ready = %v", progress.Pending, progress.ReadyToClose)
	}

	// Bulk accept uses the AI draft verbatim, so the question that was
	// reopened now carries the draft rather than the earlier human edit -
	// which is exactly what "accept the draft as written" means.
	reloaded, err := env.Repos.Questions.GetByID(ctx, first.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.AssessorFeedbackFinal != originalDraft {
		t.Error("bulk accept should sign off the AI draft as written")
	}
	if reloaded.AssessorFeedbackDraft != originalDraft {
		t.Error("bulk accept must not modify the draft column")
	}
}

// TestBulkFinalizeSkipsQuestionsWithNoDraft: accepting a blank draft would put
// an empty finding into the signed record.
func TestBulkFinalizeSkipsQuestionsWithNoDraft(t *testing.T) {
	env := newTestEnv(t)
	env.truncateAll(t)
	ctx := context.Background()

	questions := mustIngestAndReview(t, env)
	assessmentID := questions[0].AssessmentID

	// Simulate a question the AI could not draft for.
	if _, err := env.DB.Pool().Exec(ctx,
		`UPDATE questions SET assessor_feedback_draft = '', review_status = 'pending' WHERE id = $1`,
		questions[0].ID); err != nil {
		t.Fatalf("blank the draft: %v", err)
	}

	res, err := env.Assess.BulkFinalize(ctx, assessmentID, nil, nil)
	if err != nil {
		t.Fatalf("BulkFinalize: %v", err)
	}
	if res.Skipped != 1 {
		t.Errorf("Skipped = %d, want 1", res.Skipped)
	}
	if res.Finalized != len(questions)-1 {
		t.Errorf("Finalized = %d, want %d", res.Finalized, len(questions)-1)
	}

	q, _ := env.Repos.Questions.GetByID(ctx, questions[0].ID)
	if q.ReviewStatus == domain.ReviewFinalized {
		t.Error("a question with no draft was signed off with empty feedback")
	}

	progress, _ := env.Assess.Progress(ctx, assessmentID)
	if progress.NoDraft != 1 {
		t.Errorf("Progress.NoDraft = %d, want 1 so the UI can say what is left", progress.NoDraft)
	}
	if progress.ReadyToClose {
		t.Error("the assessment must not be closeable with an unsigned question")
	}
}

// TestBulkFinalizeScopedToOneDomain covers the per-domain "accept the rest"
// a reviewer uses after reading through one section.
func TestBulkFinalizeScopedToOneDomain(t *testing.T) {
	env := newTestEnv(t)
	env.truncateAll(t)
	ctx := context.Background()

	questions := mustIngestAndReview(t, env)
	assessmentID := questions[0].AssessmentID
	target := questions[0].DomainID

	inDomain := 0
	for _, q := range questions {
		if q.DomainID == target {
			inDomain++
		}
	}

	res, err := env.Assess.BulkFinalize(ctx, assessmentID, &target, nil)
	if err != nil {
		t.Fatalf("BulkFinalize: %v", err)
	}
	if res.Finalized != inDomain {
		t.Errorf("Finalized = %d, want %d (only the target domain)", res.Finalized, inDomain)
	}

	for _, q := range questions {
		reloaded, _ := env.Repos.Questions.GetByID(ctx, q.ID)
		wantFinal := q.DomainID == target
		gotFinal := reloaded.ReviewStatus == domain.ReviewFinalized
		if gotFinal != wantFinal {
			t.Errorf("question in domain %d finalized = %v, want %v", q.DomainID, gotFinal, wantFinal)
		}
	}
}

// TestCloseAndReopenAssessment covers the guard on closing and the fact that a
// closed assessment is read-only.
func TestCloseAndReopenAssessment(t *testing.T) {
	env := newTestEnv(t)
	env.truncateAll(t)
	ctx := context.Background()

	questions := mustIngestAndReview(t, env)
	assessmentID := questions[0].AssessmentID

	// Closing with work outstanding is refused, and the message says how much.
	err := env.Assess.CloseAssessment(ctx, assessmentID)
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("close error = %v, want invalid input", err)
	}
	if !strings.Contains(err.Error(), "sign-off") {
		t.Errorf("message should mention sign-off, got %q", err.Error())
	}

	if _, err := env.Assess.BulkFinalize(ctx, assessmentID, nil, nil); err != nil {
		t.Fatalf("BulkFinalize: %v", err)
	}
	if err := env.Assess.CloseAssessment(ctx, assessmentID); err != nil {
		t.Fatalf("CloseAssessment: %v", err)
	}

	a, err := env.Assess.GetAssessment(ctx, assessmentID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if a.Status != domain.StatusClosed || a.ClosedAt == nil {
		t.Errorf("status = %s, closed_at = %v", a.Status, a.ClosedAt)
	}

	// The stored upload has served its purpose; the questions are the record.
	if _, err := env.Repos.Assessments.GetUpload(ctx, assessmentID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("the upload should be discarded on close, got %v", err)
	}

	// A closed assessment is the signed record and cannot be edited.
	if _, err := env.Assess.FinalizeQuestion(ctx, questions[0].ID, "changed", nil); !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("editing a closed assessment error = %v, want invalid input", err)
	}
	if _, err := env.Assess.ReopenQuestion(ctx, questions[0].ID); !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("reopening a question in a closed assessment error = %v, want invalid input", err)
	}
	if _, err := env.Review.Enqueue(ctx, assessmentID); err == nil {
		t.Error("a review should not start on a closed assessment")
	}

	// Reopening makes it editable again.
	if err := env.Assess.ReopenAssessment(ctx, assessmentID); err != nil {
		t.Fatalf("ReopenAssessment: %v", err)
	}
	if _, err := env.Assess.ReopenQuestion(ctx, questions[0].ID); err != nil {
		t.Errorf("after reopening the assessment, editing should work: %v", err)
	}
}

// TestExportMarksUnconfirmedDrafts is the safety property of the export: an
// unsigned AI draft must never leave the tool looking like a signed finding.
func TestExportMarksUnconfirmedDrafts(t *testing.T) {
	env := newTestEnv(t)
	env.truncateAll(t)
	ctx := context.Background()

	questions := mustIngestAndReview(t, env)
	assessmentID := questions[0].AssessmentID

	// Sign one, leave the rest as drafts, and blank one entirely.
	if _, err := env.Assess.FinalizeQuestion(ctx, questions[0].ID, "Signed by the assessor.", nil); err != nil {
		t.Fatalf("finalize: %v", err)
	}
	if _, err := env.DB.Pool().Exec(ctx,
		`UPDATE questions SET assessor_feedback_draft = '' WHERE id = $1`, questions[1].ID); err != nil {
		t.Fatalf("blank a draft: %v", err)
	}

	var buf bytes.Buffer
	if err := env.Assess.ExportCSV(ctx, assessmentID, &buf); err != nil {
		t.Fatalf("ExportCSV: %v", err)
	}
	rows, err := csv.NewReader(&buf).ReadAll()
	if err != nil {
		t.Fatalf("the export is not valid CSV: %v", err)
	}
	if len(rows) != len(questions)+1 {
		t.Fatalf("got %d rows, want %d plus a header", len(rows)-1, len(questions))
	}

	col := map[string]int{}
	for i, name := range rows[0] {
		col[name] = i
	}
	for _, required := range []string{"Domain", "Question", "Risk Score", "Flags", "Assessor Feedback", "Feedback Status"} {
		if _, ok := col[required]; !ok {
			t.Fatalf("the export is missing the %q column", required)
		}
	}

	var signed, unconfirmed, missing int
	for _, row := range rows[1:] {
		status := row[col["Feedback Status"]]
		switch {
		case status == "Signed off":
			signed++
		case strings.Contains(status, "UNCONFIRMED"):
			unconfirmed++
			if row[col["Assessor Feedback"]] == "" {
				t.Error("a row marked as carrying an unconfirmed draft has no feedback text")
			}
		case strings.Contains(status, "NOT REVIEWED"):
			missing++
		default:
			t.Errorf("unrecognised feedback status %q", status)
		}
	}
	if signed != 1 {
		t.Errorf("signed rows = %d, want 1", signed)
	}
	if missing != 1 {
		t.Errorf("rows with no feedback at all = %d, want 1", missing)
	}
	if unconfirmed != len(questions)-2 {
		t.Errorf("unconfirmed rows = %d, want %d", unconfirmed, len(questions)-2)
	}
	// Every unsigned row must be visibly marked, or the export reads as a
	// finished assessment.
	if unconfirmed+missing == 0 {
		t.Fatal("nothing was marked unconfirmed despite unsigned questions")
	}
}

// TestRecomputeSummaryTracksSignOff: the summary's counters must follow human
// progress without another AI call.
func TestRecomputeSummaryTracksSignOff(t *testing.T) {
	env := newTestEnv(t)
	env.truncateAll(t)
	ctx := context.Background()

	questions := mustIngestAndReview(t, env)
	assessmentID := questions[0].AssessmentID

	before, err := env.Repos.Summaries.GetByAssessment(ctx, assessmentID)
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	if before.FinalizedCount != 0 {
		t.Fatalf("precondition: FinalizedCount = %d, want 0", before.FinalizedCount)
	}
	narrative := before.Narrative

	if _, err := env.Assess.FinalizeQuestion(ctx, questions[0].ID, "Signed.", nil); err != nil {
		t.Fatalf("finalize: %v", err)
	}
	after, err := env.Review.RecomputeSummary(ctx, assessmentID)
	if err != nil {
		t.Fatalf("RecomputeSummary: %v", err)
	}

	if after.FinalizedCount != 1 {
		t.Errorf("FinalizedCount = %d, want 1", after.FinalizedCount)
	}
	if after.PendingFinalization != len(questions)-1 {
		t.Errorf("PendingFinalization = %d, want %d", after.PendingFinalization, len(questions)-1)
	}
	// The risk figures are about the answers, not about who signed them.
	if after.OverallScore != before.OverallScore {
		t.Errorf("OverallScore changed on sign-off: %.2f -> %.2f", before.OverallScore, after.OverallScore)
	}
	// The narrative costs an AI call, so recomputing must keep the existing one.
	if after.Narrative != narrative {
		t.Error("recomputing the summary discarded the AI narrative")
	}
}
