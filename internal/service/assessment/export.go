package assessment

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"strconv"
	"strings"

	"third-party-review/internal/domain"
)

// ExportCSV writes the reviewed assessment as CSV.
//
// The export uses assessor_feedback_final where a human has signed off and
// falls back to the AI draft otherwise - and when it falls back, it says so in
// a dedicated column. An export that silently mixed signed findings with
// unreviewed AI text would be the most dangerous artefact this tool could
// produce, because it reads as a finished assessment.
func (s *Service) ExportCSV(ctx context.Context, assessmentID int64, w io.Writer) error {
	a, err := s.repos.Assessments.GetByID(ctx, assessmentID)
	if err != nil {
		return err
	}
	questions, err := s.repos.Questions.ListForReview(ctx, assessmentID)
	if err != nil {
		return err
	}
	results, err := s.repos.Results.LatestByAssessment(ctx, assessmentID, nil)
	if err != nil {
		return err
	}

	cw := csv.NewWriter(w)
	defer cw.Flush()

	header := []string{
		"Domain",
		"Question",
		"Assessor Remark",
		"Third Party Answer",
		"Third Party Remark",
		"Third Party Feedback",
		"Link Evidence",
		"Risk Score",
		"Risk Band",
		"Completeness",
		"Flags",
		"Rationale",
		"Assessor Feedback",
		"Feedback Status",
		"Signed Off At",
	}
	if err := cw.Write(header); err != nil {
		return fmt.Errorf("export: write header: %w", err)
	}

	for _, q := range questions {
		feedback, unconfirmed := q.EffectiveFeedback()

		status := "Signed off"
		switch {
		case q.ReviewStatus == domain.ReviewFinalized:
			status = "Signed off"
		case strings.TrimSpace(feedback) == "":
			status = "NOT REVIEWED - no feedback"
		case unconfirmed:
			status = "UNCONFIRMED AI DRAFT - not signed off"
		}

		var (
			score, band, completeness, flags, rationale string
		)
		if r := results[q.ID]; r != nil {
			if r.RiskScore.Valid() {
				score = strconv.Itoa(int(r.RiskScore))
				band = r.RiskScore.Band().Label()
			}
			completeness = r.Completeness.Label()
			rationale = r.Rationale
			labels := make([]string, 0, len(r.Flags))
			for _, f := range r.Flags {
				if f.Detail != "" {
					labels = append(labels, f.Kind.Label()+": "+f.Detail)
				} else {
					labels = append(labels, f.Kind.Label())
				}
			}
			flags = strings.Join(labels, " | ")
		}

		signedAt := ""
		if q.FinalizedAt != nil {
			signedAt = q.FinalizedAt.Format("2006-01-02 15:04")
		}

		row := []string{
			q.DomainName,
			q.QuestionText,
			q.AssessorRemark,
			q.ThirdPartyAnswer,
			q.ThirdPartyRemark,
			q.ThirdPartyFeedback,
			q.LinkEvidence,
			score,
			band,
			completeness,
			flags,
			rationale,
			feedback,
			status,
			signedAt,
		}
		if err := cw.Write(row); err != nil {
			return fmt.Errorf("export: write row: %w", err)
		}
	}

	cw.Flush()
	if err := cw.Error(); err != nil {
		return fmt.Errorf("export: %w", err)
	}
	s.log.Info("assessment exported", "assessment_id", assessmentID, "vendor", a.VendorName, "rows", len(questions))
	return nil
}

// ExportFilename builds a filesystem-safe filename for the export.
func ExportFilename(a *domain.Assessment) string {
	safe := func(s string) string {
		var b strings.Builder
		for _, r := range s {
			switch {
			case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
				b.WriteRune(r)
			case r == ' ' || r == '-' || r == '_':
				b.WriteByte('-')
			}
		}
		return strings.Trim(b.String(), "-")
	}
	name := safe(a.VendorName) + "-" + safe(a.Title)
	name = strings.Trim(strings.ReplaceAll(name, "--", "-"), "-")
	if name == "" {
		name = "assessment-" + strconv.FormatInt(a.ID, 10)
	}
	return name + ".csv"
}
