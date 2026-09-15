// Package integration exercises the repository, service and worker layers
// against a real PostgreSQL database. The suite skips itself when
// TEST_DATABASE_URL is unset, so `go test ./...` still passes on a machine
// with no database.
package integration

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/xuri/excelize/v2"

	"third-party-review/internal/config"
	"third-party-review/internal/helper"
	"third-party-review/internal/repository"
	"third-party-review/internal/service"
	"third-party-review/internal/service/assessment"
	"third-party-review/internal/service/auth"
	"third-party-review/internal/service/parser"
	"third-party-review/internal/service/review"
	"third-party-review/migrations"
)

// testEnv bundles everything a test needs, wired the same way main.go wires it.
type testEnv struct {
	DB       *helper.DB
	Repos    *repository.Repositories
	Assess   *assessment.Service
	Review   *review.Service
	Auth     *auth.Service
	Reviewer *mockReviewerHolder
	Log      *slog.Logger
}

// mockReviewerHolder lets a test swap reviewer behaviour between runs.
type mockReviewerHolder struct{ service.AIReviewer }

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()

	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not set; skipping database integration tests")
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	ctx := context.Background()

	db, err := helper.Connect(ctx, config.DB{
		DSN: dsn, MaxConns: 5, MinConns: 1,
		MaxConnLifetime: time.Hour, ConnectTimeout: 10 * time.Second,
	})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(db.Close)

	if err := helper.Migrate(db.Pool(), migrations.FS, ".", log); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	repos := repository.NewRepositories(db)
	aiCfg := config.AI{Provider: config.ProviderMock, Model: "mock", BatchSize: 3, MaxConcurrency: 1}
	reviewer := &mockReviewerHolder{AIReviewer: newMock()}

	return &testEnv{
		DB:    db,
		Repos: repos,
		// MustNew rather than New: a wiring mistake in a fixture should stop
		// the test run immediately, not be handled.
		Assess:   assessment.MustNew(assessment.FromRepositories(repos, parser.New(), log)),
		Review:   review.MustNew(review.FromRepositories(repos, reviewer, aiCfg, log)),
		Auth:     auth.MustNew(auth.FromRepositories(repos, time.Hour, log)),
		Reviewer: reviewer,
		Log:      log,
	}
}

// truncateAll clears every table between tests so they do not see each other's
// rows. The domain seed is re-applied because tests depend on it.
func (e *testEnv) truncateAll(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	_, err := e.DB.Pool().Exec(ctx, `
		TRUNCATE assessment_summaries, review_results, review_jobs,
		         assessment_rubrics, rubrics, questions, assessment_uploads,
		         assessments, vendors, sessions, users
		RESTART IDENTITY CASCADE`)
	if err != nil {
		t.Fatalf("truncate: %v", err)
	}
}

// seedWorkbook builds a two-domain questionnaire in the standard template, with
// the domain headings merged across the row as a real file has them.
func seedWorkbook(t *testing.T) []byte {
	t.Helper()
	rows := [][]string{
		{"Third Party Security Assessment", "", "", "", "", "", ""},
		{"Question", "Assessor Remark", "Third Party Answer", "Third Party Remark",
			"Assessor Feedback", "Third Party Feedback", "Link Evidence"},
		{"1. Network Security", "", "", "", "", "", ""},
		{"Do you segment your production network?", "Look for VLAN detail.",
			"Yes. Production sits on isolated VLANs with ACLs reviewed quarterly by the network team.",
			"Diagram on request.", "", "", "https://evidence.example/net-1"},
		{"Are firewall rules reviewed periodically?", "",
			"Industry standard firewalls are in place.", "", "", "", ""},
		{"Is remote access restricted?", "", "", "", "", "", ""},
		{"7. Cloud Security", "", "", "", "", "", ""},
		{"Which cloud regions host customer data?", "Expect named regions.",
			"AWS eu-west-1 and eu-central-1, with no replication outside the EU.", "", "", "",
			"https://evidence.example/cloud-1"},
		{"How is cloud posture monitored?", "", "", "", "", "", ""},
	}

	f := excelize.NewFile()
	defer f.Close()
	sheet := "Assessment"
	idx, err := f.NewSheet(sheet)
	if err != nil {
		t.Fatalf("NewSheet: %v", err)
	}
	f.SetActiveSheet(idx)
	f.DeleteSheet("Sheet1")

	for r, row := range rows {
		for c, val := range row {
			cell, _ := excelize.CoordinatesToCellName(c+1, r+1)
			if err := f.SetCellStr(sheet, cell, val); err != nil {
				t.Fatalf("SetCellStr: %v", err)
			}
		}
	}
	for _, r := range []int{2, 6} {
		start, _ := excelize.CoordinatesToCellName(1, r+1)
		end, _ := excelize.CoordinatesToCellName(7, r+1)
		if err := f.MergeCell(sheet, start, end); err != nil {
			t.Fatalf("MergeCell: %v", err)
		}
	}

	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		t.Fatalf("Write: %v", err)
	}
	return buf.Bytes()
}
