package assessment

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"strconv"
	"strings"

	"third-party-review/internal/dto"
	"third-party-review/internal/helper"
	"third-party-review/internal/model"

	"github.com/google/uuid"
	"github.com/xuri/excelize/v2"
)

// ExportCSV writes the reviewed assessment as CSV.
//
// The export uses assessor_feedback_final where a human has signed off and
// falls back to the AI draft otherwise - and when it falls back, it says so in
// a dedicated column. An export that silently mixed signed findings with
// unreviewed AI text would be the most dangerous artefact this tool could
// produce, because it reads as a finished assessment.
func (s *Service) ExportCSV(ctx context.Context, assessmentID uuid.UUID, w io.Writer) error {
	a, err := s.assessments.GetByID(ctx, assessmentID)
	if err != nil {
		return err
	}
	questions, err := s.questions.ListForReview(ctx, assessmentID)
	if err != nil {
		return err
	}
	results, err := s.results.LatestByAssessment(ctx, assessmentID, nil)
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
		feedback, unconfirmed := helper.EffectiveFeedback(q)

		status := "Signed off"
		switch {
		case q.ReviewStatus == model.ReviewFinalized:
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
			if helper.ValidRiskScore(r.RiskScore) {
				score = strconv.Itoa(int(r.RiskScore))
				band = helper.RiskBandLabel(helper.Band(r.RiskScore))
			}
			completeness = helper.CompletenessLabel(r.Completeness)
			rationale = r.Rationale
			labels := make([]string, 0, len(r.Flags))
			for _, f := range r.Flags {
				if f.Detail != "" {
					labels = append(labels, helper.FlagKindLabel(f.Kind)+": "+f.Detail)
				} else {
					labels = append(labels, helper.FlagKindLabel(f.Kind))
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

// ExportFilename builds a filesystem-safe filename for the CSV export.
func ExportFilename(a *model.Assessment) string {
	return exportBaseName(a) + ".csv"
}

// ExportXLSXFilename builds a filesystem-safe filename for the Excel export.
func ExportXLSXFilename(a *model.Assessment) string {
	return exportBaseName(a) + ".xlsx"
}

func exportBaseName(a *model.Assessment) string {
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
		name = "assessment-" + a.ID.String()
	}
	return name
}

// ExportXLSX writes the reviewed assessment back out in the shape of the
// originally-uploaded questionnaire: the same 7 template columns, in order,
// with each domain's section-divider row reconstructed ahead of its
// questions - nothing about those divider rows is persisted on Question, so
// they are rebuilt here from the domain each question already carries,
// watching for the boundary between one domain's block and the next, exactly
// the way the ingestion side originally detected them (see
// service/parser.DetectSections).
//
// The Assessor Feedback column carries assessor_feedback_final where a human
// has signed off, and otherwise the AI draft prefixed with a plain-text
// warning - never presented unmarked, for the same reason ExportCSV marks it:
// an unconfirmed AI opinion that reads as a finished assessment is the most
// dangerous thing this tool could hand someone.
func (s *Service) ExportXLSX(ctx context.Context, assessmentID uuid.UUID, w io.Writer) error {
	a, err := s.assessments.GetByID(ctx, assessmentID)
	if err != nil {
		return err
	}
	questions, err := s.questions.ListForReview(ctx, assessmentID)
	if err != nil {
		return err
	}

	f := excelize.NewFile()
	defer f.Close()
	const sheet = "Sheet1"
	if name := f.GetSheetName(0); name != sheet {
		f.SetSheetName(name, sheet)
	}

	headerStyle, err := f.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Bold: true},
		Fill:      excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"#E9ECEF"}},
		Alignment: &excelize.Alignment{Vertical: "center", WrapText: true},
	})
	if err != nil {
		return fmt.Errorf("export: build header style: %w", err)
	}
	dividerStyle, err := f.NewStyle(&excelize.Style{
		Font: &excelize.Font{Bold: true},
		Fill: excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"#D6DCE5"}},
	})
	if err != nil {
		return fmt.Errorf("export: build divider style: %w", err)
	}
	wrapStyle, err := f.NewStyle(&excelize.Style{
		Alignment: &excelize.Alignment{Vertical: "top", WrapText: true},
	})
	if err != nil {
		return fmt.Errorf("export: build cell style: %w", err)
	}

	cols := dto.TemplateFields
	lastCol, err := excelize.ColumnNumberToName(len(cols))
	if err != nil {
		return fmt.Errorf("export: resolve column range: %w", err)
	}

	for i, tf := range cols {
		cell, err := excelize.CoordinatesToCellName(i+1, 1)
		if err != nil {
			return fmt.Errorf("export: header cell: %w", err)
		}
		if err := f.SetCellStr(sheet, cell, tf.Label); err != nil {
			return fmt.Errorf("export: write header: %w", err)
		}
	}
	if err := f.SetCellStyle(sheet, "A1", lastCol+"1", headerStyle); err != nil {
		return fmt.Errorf("export: style header: %w", err)
	}
	f.SetColWidth(sheet, "A", "A", 40)
	f.SetColWidth(sheet, "B", "D", 32)
	f.SetColWidth(sheet, "E", "F", 40)
	f.SetColWidth(sheet, "G", "G", 24)

	row := 2
	var currentDomain uuid.UUID
	haveDomain := false

	writeCell := func(col int, value string) error {
		cell, err := excelize.CoordinatesToCellName(col, row)
		if err != nil {
			return err
		}
		return f.SetCellStr(sheet, cell, value)
	}

	for _, q := range questions {
		if !haveDomain || q.DomainID != currentDomain {
			// A divider row is a single label merged across every template
			// column, mirroring how the original template lays a domain
			// heading across the row it detects one from (see
			// parser.DetectSections's merge-expansion handling).
			name := q.DomainName
			if strings.TrimSpace(name) == "" {
				name = "Domain"
			}
			startCell, err := excelize.CoordinatesToCellName(1, row)
			if err != nil {
				return fmt.Errorf("export: divider cell: %w", err)
			}
			endCell, err := excelize.CoordinatesToCellName(len(cols), row)
			if err != nil {
				return fmt.Errorf("export: divider cell: %w", err)
			}
			if err := f.SetCellStr(sheet, startCell, name); err != nil {
				return fmt.Errorf("export: write divider: %w", err)
			}
			if err := f.MergeCell(sheet, startCell, endCell); err != nil {
				return fmt.Errorf("export: merge divider: %w", err)
			}
			if err := f.SetCellStyle(sheet, startCell, endCell, dividerStyle); err != nil {
				return fmt.Errorf("export: style divider: %w", err)
			}
			currentDomain, haveDomain = q.DomainID, true
			row++
		}

		feedback, unconfirmed := helper.EffectiveFeedback(q)
		if unconfirmed && strings.TrimSpace(feedback) != "" {
			feedback = "[DRAFT - NOT YET SIGNED OFF] " + feedback
		}

		values := map[model.QuestionField]string{
			model.FieldQuestion:           q.QuestionText,
			model.FieldAssessorRemark:     q.AssessorRemark,
			model.FieldThirdPartyAnswer:   q.ThirdPartyAnswer,
			model.FieldThirdPartyRemark:   q.ThirdPartyRemark,
			model.FieldAssessorFeedback:   feedback,
			model.FieldThirdPartyFeedback: q.ThirdPartyFeedback,
			model.FieldLinkEvidence:       q.LinkEvidence,
		}
		for i, tf := range cols {
			if err := writeCell(i+1, values[tf.Field]); err != nil {
				return fmt.Errorf("export: write cell: %w", err)
			}
		}
		startCell, err := excelize.CoordinatesToCellName(1, row)
		if err != nil {
			return fmt.Errorf("export: row style: %w", err)
		}
		endCell, err := excelize.CoordinatesToCellName(len(cols), row)
		if err != nil {
			return fmt.Errorf("export: row style: %w", err)
		}
		if err := f.SetCellStyle(sheet, startCell, endCell, wrapStyle); err != nil {
			return fmt.Errorf("export: style row: %w", err)
		}
		row++
	}

	if _, err := f.WriteTo(w); err != nil {
		return fmt.Errorf("export: %w", err)
	}
	s.log.Info("assessment exported as xlsx", "assessment_id", assessmentID, "vendor", a.VendorName, "rows", len(questions))
	return nil
}
