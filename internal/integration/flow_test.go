package integration

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"third-party-review/internal/dto"
	"third-party-review/internal/helper"
	"third-party-review/internal/model"

	"github.com/google/uuid"
)

// TestSchemaSeedsEightDomains pins the migration contract the parser depends
// on: the domains are data, and all eight must be present after migrating.
func TestSchemaSeedsEightDomains(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	domains, err := env.Repos.Domains.List(ctx, false)
	if err != nil {
		t.Fatalf("list domains: %v", err)
	}
	if len(domains) != 8 {
		t.Fatalf("got %d seeded domains, want 8", len(domains))
	}
	for i, want := range helper.SeededDomainNames {
		if domains[i].Name != want {
			t.Errorf("domain %d = %q, want %q (seed order must match)", i, domains[i].Name, want)
		}
		if domains[i].ScrutinyNote == "" {
			t.Errorf("domain %q has no scrutiny note; the AI prompt relies on it", want)
		}
	}
}

// TestSeedMigrationIsIdempotent guards the ON CONFLICT clause: re-running
// migrations must not duplicate the domains.
func TestSeedMigrationIsIdempotent(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	_, err := env.DB.Pool().Exec(ctx, `
		INSERT INTO assessment_domains (name, slug, sort_order)
		VALUES ('Network Security', 'network-security', 1)
		ON CONFLICT (slug) DO NOTHING`)
	if err != nil {
		t.Fatalf("re-seed: %v", err)
	}
	domains, err := env.Repos.Domains.List(ctx, false)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(domains) != 8 {
		t.Errorf("re-seeding produced %d domains, want 8", len(domains))
	}
}

// TestFullIngestToReviewFlow walks the whole pipeline the way a user does:
// create a vendor, upload a questionnaire, confirm the mapping, run the AI
// review, and check what lands in the database.
func TestFullIngestToReviewFlow(t *testing.T) {
	env := newTestEnv(t)
	env.truncateAll(t)
	ctx := context.Background()

	vendor := &model.Vendor{Name: "Acme Corp", ContactEmail: "security@acme.example"}
	if err := env.Assess.CreateVendor(ctx, vendor); err != nil {
		t.Fatalf("create vendor: %v", err)
	}

	// --- upload -------------------------------------------------------------
	a, err := env.Assess.Upload(ctx, vendor.ID, "", "acme_tpsa_2026.xlsx",
		"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
		bytes.NewReader(seedWorkbook(t)))
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if a.Status != model.StatusUploaded {
		t.Errorf("status after upload = %s, want uploaded", a.Status)
	}
	if a.Title != "acme tpsa 2026" {
		t.Errorf("derived title = %q, want the filename without extension", a.Title)
	}
	if a.SourceSHA256 == "" {
		t.Error("upload checksum was not recorded")
	}

	// --- preview ------------------------------------------------------------
	_, preview, err := env.Assess.Preview(ctx, a.ID, "")
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if preview.QuestionCount != 5 {
		t.Fatalf("preview found %d questions, want 5", preview.QuestionCount)
	}
	if len(preview.Sections) != 2 {
		t.Fatalf("preview found %d sections, want 2: %+v", len(preview.Sections), preview.Sections)
	}
	for _, tf := range dto.TemplateFields {
		if _, ok := preview.Mapping.Bindings[tf.Field]; !ok {
			t.Errorf("template field %s was not auto-mapped from the standard header", tf.Field)
		}
	}

	// --- confirm mapping ----------------------------------------------------
	n, err := env.Assess.ConfirmMapping(ctx, a.ID, preview.Mapping, nil, "")
	if err != nil {
		t.Fatalf("confirm mapping: %v", err)
	}
	if n != 5 {
		t.Fatalf("ingested %d questions, want 5", n)
	}

	a, err = env.Assess.GetAssessment(ctx, a.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if a.Status != model.StatusMapped {
		t.Errorf("status after mapping = %s, want mapped", a.Status)
	}
	if a.ColumnMapping == nil || a.ColumnMapping.ConfirmedAt == "" {
		t.Error("the confirmed mapping was not persisted for audit")
	}
	if a.MappedAt == nil {
		t.Error("mapped_at was not stamped")
	}

	questions, err := env.Repos.Questions.ListForReview(ctx, a.ID)
	if err != nil {
		t.Fatalf("list questions: %v", err)
	}
	if len(questions) != 5 {
		t.Fatalf("persisted %d questions, want 5", len(questions))
	}
	// Every question must belong to exactly one domain, with the name joined.
	for _, q := range questions {
		if q.DomainID == uuid.Nil || q.DomainName == "" {
			t.Errorf("question %q has no domain", q.QuestionText)
		}
		if q.ReviewStatus != model.ReviewPending {
			t.Errorf("question %q starts at %s, want pending", q.QuestionText, q.ReviewStatus)
		}
	}
	// The unanswered rows must survive ingestion rather than being dropped.
	blank := 0
	for _, q := range questions {
		if helper.AnswerIsBlank(q) {
			blank++
		}
	}
	if blank != 2 {
		t.Errorf("got %d unanswered questions, want 2 (they must not be dropped)", blank)
	}

	// --- AI review ----------------------------------------------------------
	job, err := env.Review.Enqueue(ctx, a.ID)
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if job.TotalQuestions != 5 {
		t.Errorf("job total = %d, want 5", job.TotalQuestions)
	}
	if job.Provider != "mock" {
		t.Errorf("job provider = %q, want mock", job.Provider)
	}

	// A second enqueue must be refused while one is outstanding.
	if _, err := env.Review.Enqueue(ctx, a.ID); err == nil {
		t.Error("a duplicate review was allowed while one was already queued")
	} else if !errors.Is(err, helper.ErrInvalidInput) {
		t.Errorf("duplicate enqueue error = %v, want an invalid-input error", err)
	}

	claimed, err := env.Repos.Jobs.ClaimNext(ctx)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if err := runJob(t, env, claimed); err != nil {
		t.Fatalf("run review: %v", err)
	}

	// --- results ------------------------------------------------------------
	a, err = env.Assess.GetAssessment(ctx, a.ID)
	if err != nil {
		t.Fatalf("reload after review: %v", err)
	}
	if a.Status != model.StatusReviewed {
		t.Errorf("status after review = %s, want reviewed", a.Status)
	}
	if a.CurrentRunID == nil || *a.CurrentRunID != claimed.ID {
		t.Error("the assessment does not point at the run that produced its results")
	}

	results, err := env.Repos.Results.LatestByAssessment(ctx, a.ID, nil)
	if err != nil {
		t.Fatalf("latest results: %v", err)
	}
	if len(results) != 5 {
		t.Fatalf("got %d results, want one per question", len(results))
	}
	for _, q := range questions {
		r := results[q.ID]
		if r == nil {
			t.Fatalf("question %q has no result", q.QuestionText)
		}
		if !helper.ValidRiskScore(r.RiskScore) {
			t.Errorf("question %q scored %d, outside the 1-5 scale", q.QuestionText, r.RiskScore)
		}
		if r.FeedbackDraft == "" {
			t.Errorf("question %q has no draft feedback", q.QuestionText)
		}
		if r.RunID != claimed.ID {
			t.Errorf("result for %q is keyed to run %d, want %d", q.QuestionText, r.RunID, claimed.ID)
		}
		// An unanswered question must be scored at the top of the scale and
		// flagged, not quietly passed.
		if helper.AnswerIsBlank(q) {
			if r.RiskScore != 5 {
				t.Errorf("unanswered question %q scored %d, want 5", q.QuestionText, r.RiskScore)
			}
			if r.Completeness != model.CompletenessMissing {
				t.Errorf("unanswered question %q completeness = %s, want missing", q.QuestionText, r.Completeness)
			}
			if !helper.HasFlag(r, model.FlagMissingAnswer) {
				t.Errorf("unanswered question %q was not flagged", q.QuestionText)
			}
		}
	}

	// Drafts must be copied onto the questions and the status advanced.
	questions, err = env.Repos.Questions.ListForReview(ctx, a.ID)
	if err != nil {
		t.Fatalf("reload questions: %v", err)
	}
	for _, q := range questions {
		if q.AssessorFeedbackDraft == "" {
			t.Errorf("question %q has no draft on the question row", q.QuestionText)
		}
		if q.ReviewStatus != model.ReviewAIDrafted {
			t.Errorf("question %q is %s, want ai_drafted", q.QuestionText, q.ReviewStatus)
		}
		if q.AssessorFeedbackFinal != "" {
			t.Errorf("question %q has final feedback before any human signed off", q.QuestionText)
		}
	}

	// --- summary ------------------------------------------------------------
	if a.Summary == nil {
		t.Fatal("no assessment summary was stored")
	}
	s := a.Summary
	if s.QuestionCount != 5 || s.ScoredCount != 5 {
		t.Errorf("summary counts = %d/%d, want 5/5", s.ScoredCount, s.QuestionCount)
	}
	if s.IncompleteCount < 2 {
		t.Errorf("summary incomplete = %d, want at least 2 (the unanswered questions)", s.IncompleteCount)
	}
	if s.PendingFinalization != 5 {
		t.Errorf("summary pending sign-off = %d, want 5", s.PendingFinalization)
	}
	if s.OverallScore <= 0 || s.OverallScore > 5 {
		t.Errorf("overall score = %.2f, outside the scale", s.OverallScore)
	}
	if len(s.DomainScores) != 2 {
		t.Errorf("got %d domain scores, want 2", len(s.DomainScores))
	}
	if s.Narrative == "" {
		t.Error("no narrative was stored")
	}
}

// TestFinalizePreservesDraft is the core human-in-the-loop guarantee: signing
// off must never destroy what the AI originally wrote.
func TestFinalizePreservesDraft(t *testing.T) {
	env := newTestEnv(t)
	env.truncateAll(t)
	ctx := context.Background()

	questions := mustIngestAndReview(t, env)
	q := questions[0]
	originalDraft := q.AssessorFeedbackDraft
	if originalDraft == "" {
		t.Fatal("precondition: the question has no draft")
	}

	edited := "Edited by the assessor: the vendor's answer is acceptable for this cycle."
	if err := env.Repos.Questions.Finalize(ctx, q.ID, edited, nil, time.Now().UTC()); err != nil {
		t.Fatalf("finalize: %v", err)
	}

	reloaded, err := env.Repos.Questions.GetByID(ctx, q.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.AssessorFeedbackFinal != edited {
		t.Errorf("final feedback = %q, want the edited text", reloaded.AssessorFeedbackFinal)
	}
	if reloaded.AssessorFeedbackDraft != originalDraft {
		t.Error("finalizing overwrote the AI draft; the original wording must always be recoverable")
	}
	if reloaded.ReviewStatus != model.ReviewFinalized {
		t.Errorf("status = %s, want finalized", reloaded.ReviewStatus)
	}
	if reloaded.FinalizedAt == nil {
		t.Error("finalized_at was not stamped")
	}

	text, unconfirmed := helper.EffectiveFeedback(reloaded)
	if text != edited || unconfirmed {
		t.Errorf("EffectiveFeedback = (%q, unconfirmed=%v), want the signed-off text marked confirmed", text, unconfirmed)
	}

	// Empty feedback must be refused: a signed-off finding needs words.
	if err := env.Repos.Questions.Finalize(ctx, q.ID, "   ", nil, time.Now().UTC()); err == nil {
		t.Error("finalizing with blank feedback should be refused")
	}

	// Reopening keeps the text but returns the question to the draft state.
	if err := env.Repos.Questions.Unfinalize(ctx, q.ID); err != nil {
		t.Fatalf("unfinalize: %v", err)
	}
	reloaded, _ = env.Repos.Questions.GetByID(ctx, q.ID)
	if reloaded.ReviewStatus != model.ReviewAIDrafted {
		t.Errorf("status after reopening = %s, want ai_drafted", reloaded.ReviewStatus)
	}
	if reloaded.AssessorFeedbackFinal != edited {
		t.Error("reopening discarded the human's edits")
	}
}

// TestReReviewPreservesHistoryAndSignOff covers the two things a second run
// must not do: erase the first run's results, or silently reopen work a human
// has already signed off.
func TestReReviewPreservesHistoryAndSignOff(t *testing.T) {
	env := newTestEnv(t)
	env.truncateAll(t)
	ctx := context.Background()

	questions := mustIngestAndReview(t, env)
	q := questions[0]
	assessmentID := q.AssessmentID

	signedOff := "Signed off in round one."
	if err := env.Repos.Questions.Finalize(ctx, q.ID, signedOff, nil, time.Now().UTC()); err != nil {
		t.Fatalf("finalize: %v", err)
	}

	firstRun, err := env.Repos.Jobs.LatestByAssessment(ctx, assessmentID)
	if err != nil {
		t.Fatalf("latest job: %v", err)
	}

	// Second run.
	job, err := env.Review.Enqueue(ctx, assessmentID)
	if err != nil {
		t.Fatalf("enqueue second review: %v", err)
	}
	claimed, err := env.Repos.Jobs.ClaimNext(ctx)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if claimed.ID != job.ID {
		t.Fatalf("claimed job %d, want the newly queued %d", claimed.ID, job.ID)
	}
	if err := runJob(t, env, claimed); err != nil {
		t.Fatalf("second run: %v", err)
	}

	history, err := env.Repos.Results.HistoryByQuestion(ctx, q.ID)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(history) != 2 {
		t.Fatalf("got %d results in history, want 2 - a re-review must not overwrite the first", len(history))
	}
	runs := map[uuid.UUID]bool{}
	for _, h := range history {
		runs[h.RunID] = true
	}
	if !runs[firstRun.ID] || !runs[claimed.ID] {
		t.Errorf("history does not contain both runs: %v", runs)
	}

	reloaded, err := env.Repos.Questions.GetByID(ctx, q.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.ReviewStatus != model.ReviewFinalized {
		t.Errorf("a second review reopened signed-off work: status = %s", reloaded.ReviewStatus)
	}
	if reloaded.AssessorFeedbackFinal != signedOff {
		t.Error("a second review overwrote human-signed-off feedback")
	}
}

// TestBatchFailureFallsBackToPerQuestion proves the review survives a provider
// that fails a whole batch: the remaining answers are still reviewed one by one.
func TestBatchFailureFallsBackToPerQuestion(t *testing.T) {
	env := newTestEnv(t)
	env.truncateAll(t)
	ctx := context.Background()

	assessmentID := mustIngest(t, env)
	// Fail every batch containing the segmentation question, which is in the
	// first domain, so at least one batch call fails outright.
	env.Reviewer.AIReviewer = newFailingMock("segment your production network")

	job, err := env.Review.Enqueue(ctx, assessmentID)
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	claimed, _ := env.Repos.Jobs.ClaimNext(ctx)
	if err := runJob(t, env, claimed); err != nil {
		t.Fatalf("run should survive a failing batch: %v", err)
	}
	_ = job

	results, err := env.Repos.Results.LatestByAssessment(ctx, assessmentID, nil)
	if err != nil {
		t.Fatalf("results: %v", err)
	}
	if len(results) != 5 {
		t.Errorf("got %d results after a batch failure, want 5 via the per-question fallback", len(results))
	}
}

// TestDroppedQuestionsAreRecovered covers a model that silently omits items
// from a batch response.
func TestDroppedQuestionsAreRecovered(t *testing.T) {
	env := newTestEnv(t)
	env.truncateAll(t)
	ctx := context.Background()

	assessmentID := mustIngest(t, env)
	questions, err := env.Repos.Questions.ListForReview(ctx, assessmentID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	env.Reviewer.AIReviewer = newDroppingMock(questions[0].ID, questions[3].ID)

	if _, err := env.Review.Enqueue(ctx, assessmentID); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	claimed, _ := env.Repos.Jobs.ClaimNext(ctx)
	if err := runJob(t, env, claimed); err != nil {
		t.Fatalf("run: %v", err)
	}

	results, err := env.Repos.Results.LatestByAssessment(ctx, assessmentID, nil)
	if err != nil {
		t.Fatalf("results: %v", err)
	}
	if len(results) != len(questions) {
		t.Errorf("got %d results, want %d - dropped items must be retried individually",
			len(results), len(questions))
	}
}

// TestReviewRefusedBeforeMapping stops a review being started against an
// assessment that has no questions yet.
func TestReviewRefusedBeforeMapping(t *testing.T) {
	env := newTestEnv(t)
	env.truncateAll(t)
	ctx := context.Background()

	vendor := &model.Vendor{Name: "Unmapped Ltd"}
	if err := env.Assess.CreateVendor(ctx, vendor); err != nil {
		t.Fatalf("create vendor: %v", err)
	}
	a, err := env.Assess.Upload(ctx, vendor.ID, "Unmapped", "q.xlsx", "", bytes.NewReader(seedWorkbook(t)))
	if err != nil {
		t.Fatalf("upload: %v", err)
	}

	_, err = env.Review.Enqueue(ctx, a.ID)
	if err == nil {
		t.Fatal("a review was allowed on an unmapped assessment")
	}
	if !errors.Is(err, helper.ErrInvalidInput) {
		t.Errorf("error = %v, want invalid input", err)
	}
	if !strings.Contains(err.Error(), "Map the questionnaire first") {
		t.Errorf("error message should tell the user what to do, got %q", err.Error())
	}
}

// TestRemappingClearsStaleResults: re-ingesting replaces the question rows, so
// results keyed to the old rows must go with them rather than being orphaned.
func TestRemappingClearsStaleResults(t *testing.T) {
	env := newTestEnv(t)
	env.truncateAll(t)
	ctx := context.Background()

	questions := mustIngestAndReview(t, env)
	assessmentID := questions[0].AssessmentID

	_, preview, err := env.Assess.Preview(ctx, assessmentID, "")
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if _, err := env.Assess.ConfirmMapping(ctx, assessmentID, preview.Mapping, nil, ""); err != nil {
		t.Fatalf("re-confirm mapping: %v", err)
	}

	results, err := env.Repos.Results.LatestByAssessment(ctx, assessmentID, nil)
	if err != nil {
		t.Fatalf("results: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("got %d results after re-mapping, want 0 - stale results must not survive", len(results))
	}
	fresh, err := env.Repos.Questions.ListForReview(ctx, assessmentID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(fresh) != 5 {
		t.Errorf("got %d questions after re-mapping, want 5", len(fresh))
	}
	for _, q := range fresh {
		if q.ReviewStatus != model.ReviewPending {
			t.Errorf("re-ingested question is %s, want pending", q.ReviewStatus)
		}
	}
}

// TestRubricAttachAndDetach covers the optional rubric lifecycle.
func TestRubricAttachAndDetach(t *testing.T) {
	env := newTestEnv(t)
	env.truncateAll(t)
	ctx := context.Background()

	assessmentID := mustIngest(t, env)

	rubric := &model.Rubric{
		Name:     "Vendor Security Policy v3",
		Content:  strings.Repeat("Encryption at rest must be AES-256 or stronger.\n", 5),
		Reusable: true,
	}
	if err := env.Assess.AttachRubric(ctx, assessmentID, rubric); err != nil {
		t.Fatalf("attach: %v", err)
	}
	if rubric.ID == uuid.Nil {
		t.Fatal("the rubric was not assigned an id")
	}

	got, err := env.Assess.GetRubric(ctx, assessmentID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != rubric.Name {
		t.Errorf("rubric name = %q, want %q", got.Name, rubric.Name)
	}

	// The excerpt must be bounded so a long policy cannot blow the context.
	if len([]rune(helper.RubricExcerpt(got, 50))) > 70 {
		t.Errorf("Excerpt(50) returned %d runes, want it bounded", len([]rune(helper.RubricExcerpt(got, 50))))
	}

	// A queued job records which rubric it will use.
	job, err := env.Review.Enqueue(ctx, assessmentID)
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if job.RubricID == nil || *job.RubricID != rubric.ID {
		t.Error("the queued job does not reference the attached rubric")
	}

	if err := env.Assess.DetachRubric(ctx, assessmentID); err != nil {
		t.Fatalf("detach: %v", err)
	}
	if _, err := env.Assess.GetRubric(ctx, assessmentID); !errors.Is(err, helper.ErrNotFound) {
		t.Errorf("after detaching, GetRubric error = %v, want not found", err)
	}
	// A reusable rubric survives detaching.
	reusable, err := env.Assess.ListReusableRubrics(ctx)
	if err != nil {
		t.Fatalf("list reusable: %v", err)
	}
	if len(reusable) != 1 {
		t.Errorf("got %d reusable rubrics, want 1", len(reusable))
	}
}

// TestJobQueueClaimsOnce checks the SKIP LOCKED claim: two workers pulling at
// once must not both get the same job.
func TestJobQueueClaimsOnce(t *testing.T) {
	env := newTestEnv(t)
	env.truncateAll(t)
	ctx := context.Background()

	assessmentID := mustIngest(t, env)
	if _, err := env.Review.Enqueue(ctx, assessmentID); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	first, err := env.Repos.Jobs.ClaimNext(ctx)
	if err != nil {
		t.Fatalf("first claim: %v", err)
	}
	if first.Status != model.JobRunning {
		t.Errorf("claimed job status = %s, want running", first.Status)
	}
	if first.StartedAt == nil {
		t.Error("claiming did not stamp started_at")
	}

	if _, err := env.Repos.Jobs.ClaimNext(ctx); !errors.Is(err, helper.ErrNotFound) {
		t.Errorf("second claim error = %v, want not found - a job must be claimed once", err)
	}

	if err := env.Repos.Jobs.UpdateProgress(ctx, first.ID, 3, 1, "Reviewing Cloud Security"); err != nil {
		t.Fatalf("progress: %v", err)
	}
	reloaded, err := env.Repos.Jobs.GetByID(ctx, first.ID)
	if err != nil {
		t.Fatalf("reload job: %v", err)
	}
	if reloaded.DoneQuestions != 3 || reloaded.FailedQuestions != 1 {
		t.Errorf("progress = %d done / %d failed, want 3/1", reloaded.DoneQuestions, reloaded.FailedQuestions)
	}
	if helper.JobPercent(reloaded) != 60 {
		t.Errorf("Percent() = %d, want 60", helper.JobPercent(reloaded))
	}

	if err := env.Repos.Jobs.Finish(ctx, first.ID, model.JobSucceeded, ""); err != nil {
		t.Fatalf("finish: %v", err)
	}
	reloaded, _ = env.Repos.Jobs.GetByID(ctx, first.ID)
	if !helper.TerminalJob(reloaded.Status) || reloaded.FinishedAt == nil {
		t.Error("finishing did not record a terminal state")
	}
}

// TestStalledJobIsReclaimed covers the restart case: a job left running with a
// dead worker must be re-queued rather than stranding the assessment.
func TestStalledJobIsReclaimed(t *testing.T) {
	env := newTestEnv(t)
	env.truncateAll(t)
	ctx := context.Background()

	assessmentID := mustIngest(t, env)
	if _, err := env.Review.Enqueue(ctx, assessmentID); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	claimed, err := env.Repos.Jobs.ClaimNext(ctx)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}

	// Simulate the worker dying: push the heartbeat into the past.
	if _, err := env.DB.Pool().Exec(ctx,
		`UPDATE review_jobs SET heartbeat_at = now() - interval '10 minutes' WHERE id = $1`, claimed.ID); err != nil {
		t.Fatalf("age heartbeat: %v", err)
	}

	n, err := env.Repos.Jobs.ReclaimStalled(ctx, 2*time.Minute)
	if err != nil {
		t.Fatalf("reclaim: %v", err)
	}
	if n != 1 {
		t.Fatalf("reclaimed %d jobs, want 1", n)
	}
	reloaded, _ := env.Repos.Jobs.GetByID(ctx, claimed.ID)
	if reloaded.Status != model.JobQueued {
		t.Errorf("reclaimed job status = %s, want queued", reloaded.Status)
	}
	// And it must be claimable again.
	if _, err := env.Repos.Jobs.ClaimNext(ctx); err != nil {
		t.Errorf("a reclaimed job could not be re-claimed: %v", err)
	}
}

// TestTransactionRollsBackOnError checks the tx manager actually rolls back.
func TestTransactionRollsBackOnError(t *testing.T) {
	env := newTestEnv(t)
	env.truncateAll(t)
	ctx := context.Background()

	sentinel := errors.New("deliberate failure")
	err := env.Repos.Tx.RunInTx(ctx, func(ctx context.Context) error {
		if err := env.Repos.Vendors.Create(ctx, &model.Vendor{Name: "Rollback Ltd"}); err != nil {
			return err
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("RunInTx error = %v, want the sentinel", err)
	}
	n, err := env.Repos.Vendors.Count(ctx, "")
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Errorf("%d vendors survived a rolled-back transaction, want 0", n)
	}
}

// TestDuplicateVendorNameIsRejected covers the unique index and its mapping to
// a user-facing message.
func TestDuplicateVendorNameIsRejected(t *testing.T) {
	env := newTestEnv(t)
	env.truncateAll(t)
	ctx := context.Background()

	if err := env.Assess.CreateVendor(ctx, &model.Vendor{Name: "Acme Corp"}); err != nil {
		t.Fatalf("first create: %v", err)
	}
	// Case-insensitive: the index is on lower(name).
	err := env.Assess.CreateVendor(ctx, &model.Vendor{Name: "ACME CORP"})
	if err == nil {
		t.Fatal("a duplicate vendor name was accepted")
	}
	var ve helper.ValidationError
	if !errors.As(err, &ve) || ve.Field != "name" {
		t.Errorf("error = %v, want a field-level validation error on name", err)
	}
}

// TestCascadeDeleteRemovesEverything: deleting a vendor must not leave orphans.
func TestCascadeDeleteRemovesEverything(t *testing.T) {
	env := newTestEnv(t)
	env.truncateAll(t)
	ctx := context.Background()

	questions := mustIngestAndReview(t, env)
	assessmentID := questions[0].AssessmentID
	a, err := env.Repos.Assessments.GetByID(ctx, assessmentID)
	if err != nil {
		t.Fatalf("get assessment: %v", err)
	}

	if err := env.Assess.DeleteVendor(ctx, a.VendorID); err != nil {
		t.Fatalf("delete vendor: %v", err)
	}

	for _, check := range []struct {
		name  string
		query string
	}{
		{"assessments", "SELECT COUNT(*) FROM assessments"},
		{"questions", "SELECT COUNT(*) FROM questions"},
		{"review_results", "SELECT COUNT(*) FROM review_results"},
		{"review_jobs", "SELECT COUNT(*) FROM review_jobs"},
		{"assessment_summaries", "SELECT COUNT(*) FROM assessment_summaries"},
		{"assessment_uploads", "SELECT COUNT(*) FROM assessment_uploads"},
	} {
		var n int
		if err := env.DB.Pool().QueryRow(ctx, check.query).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", check.name, err)
		}
		if n != 0 {
			t.Errorf("%d rows left in %s after deleting the vendor", n, check.name)
		}
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// runJob mirrors what the background worker does: run the job, then record its
// terminal state. Tests go through this rather than calling Run directly so
// they exercise the same job lifecycle production does.
func runJob(t *testing.T, env *testEnv, job *model.ReviewJob) error {
	t.Helper()
	ctx := context.Background()
	runErr := env.Review.Run(ctx, job)
	if err := env.Review.Finish(ctx, job, runErr); err != nil {
		t.Fatalf("finish job: %v", err)
	}
	return runErr
}

func mustIngest(t *testing.T, env *testEnv) uuid.UUID {
	t.Helper()
	ctx := context.Background()

	vendor := &model.Vendor{Name: "Acme Corp"}
	if err := env.Assess.CreateVendor(ctx, vendor); err != nil {
		t.Fatalf("create vendor: %v", err)
	}
	a, err := env.Assess.Upload(ctx, vendor.ID, "Annual review", "tpsa.xlsx", "",
		bytes.NewReader(seedWorkbook(t)))
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	_, preview, err := env.Assess.Preview(ctx, a.ID, "")
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if _, err := env.Assess.ConfirmMapping(ctx, a.ID, preview.Mapping, nil, ""); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	return a.ID
}

func mustIngestAndReview(t *testing.T, env *testEnv) []*model.Question {
	t.Helper()
	ctx := context.Background()

	assessmentID := mustIngest(t, env)
	if _, err := env.Review.Enqueue(ctx, assessmentID); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	claimed, err := env.Repos.Jobs.ClaimNext(ctx)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if err := runJob(t, env, claimed); err != nil {
		t.Fatalf("run: %v", err)
	}
	questions, err := env.Repos.Questions.ListForReview(ctx, assessmentID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	return questions
}

// TestListPopulatesRiskColumn: the assessment index shows a risk figure per
// row. It is joined in the list query rather than fetched per row, and a
// regression there shows up as a column of dashes on the busiest page in the
// app - which reads as "not reviewed" rather than "we forgot to load it".
func TestListPopulatesRiskColumn(t *testing.T) {
	env := newTestEnv(t)
	env.truncateAll(t)
	ctx := context.Background()

	questions := mustIngestAndReview(t, env)
	assessmentID := questions[0].AssessmentID

	// A second assessment that has been uploaded but never reviewed.
	vendor := &model.Vendor{Name: "Unreviewed Ltd"}
	if err := env.Assess.CreateVendor(ctx, vendor); err != nil {
		t.Fatalf("create vendor: %v", err)
	}
	if _, err := env.Assess.Upload(ctx, vendor.ID, "Not yet reviewed", "q.xlsx", "",
		bytes.NewReader(seedWorkbook(t))); err != nil {
		t.Fatalf("upload: %v", err)
	}

	items, _, err := env.Assess.ListAssessments(ctx, dto.AssessmentFilter{})
	if err != nil {
		t.Fatalf("ListAssessments: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("got %d assessments, want 2", len(items))
	}

	var reviewed, unreviewed *model.Assessment
	for _, a := range items {
		if a.ID == assessmentID {
			reviewed = a
		} else {
			unreviewed = a
		}
	}

	if reviewed.Summary == nil {
		t.Fatal("a reviewed assessment has no summary in the list; the risk column would show a dash")
	}
	if reviewed.Summary.OverallScore <= 0 {
		t.Errorf("OverallScore = %.2f, want a real score", reviewed.Summary.OverallScore)
	}
	if reviewed.Summary.QuestionCount != len(questions) {
		t.Errorf("QuestionCount = %d, want %d", reviewed.Summary.QuestionCount, len(questions))
	}

	// An assessment with no review must stay nil rather than reporting 0.0,
	// which would render as a genuine "no risk" score.
	if unreviewed.Summary != nil {
		t.Errorf("an unreviewed assessment carries a summary %+v; it should be nil", unreviewed.Summary)
	}
}
