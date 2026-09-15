package parser

import (
	"io"
	"strings"

	"third-party-review/internal/dto"
	"third-party-review/internal/helper"
	"third-party-review/internal/model"
	"third-party-review/internal/service"

	"github.com/google/uuid"
)

// Parser turns uploaded files into a confirmable Preview. It is stateless and
// safe for concurrent use.
type Parser struct{}

// New returns a Parser.
func New() *Parser { return &Parser{} }

// Parser implements the domain contract, so the ingestion service depends on
// the interface rather than on this package.
var _ service.QuestionnaireParser = (*Parser)(nil)

// Parse reads a file and produces the best-guess preview: header row, column
// mapping, classified rows and detected domain sections. It never writes to
// the database; the caller shows the preview, takes the user's corrections,
// and calls Apply with the confirmed mapping.
func (p *Parser) Parse(r io.Reader, filename string, domains []*model.AssessmentDomain) (*dto.IngestPreview, error) {
	grid, err := ReadGrid(r, filename)
	if err != nil {
		return nil, err
	}
	return p.PreviewGrid(grid, domains)
}

// PreviewGrid builds a preview from an already-read grid. Split out so the
// user can switch worksheets without re-uploading.
func (p *Parser) PreviewGrid(grid *dto.Grid, domains []*model.AssessmentDomain) (*dto.IngestPreview, error) {
	if len(grid.Rows) == 0 {
		return nil, helper.NewParseError("The sheet has no rows.", "Pick a different worksheet.", nil)
	}

	headerRow, headerScore := DetectHeaderRow(grid)
	mapping, candidates := AutoMap(grid, headerRow)

	var warnings []string
	if headerScore < 0.2 {
		warnings = append(warnings,
			"The header row was hard to identify. Check the highlighted row is the real header.")
	}

	// Without a Question column nothing else can be placed, so try the
	// content-based fallback before giving up on the file.
	if _, ok := mapping.Bindings[model.FieldQuestion]; !ok {
		if col, ok := FallbackQuestionColumn(grid, headerRow); ok {
			mapping.Bindings[model.FieldQuestion] = model.ColumnBinding{
				Field:      model.FieldQuestion,
				Index:      col,
				Header:     strings.TrimSpace(grid.Cell(headerRow, col)),
				Confidence: 0,
			}
			for i := range candidates {
				if candidates[i].Index == col {
					candidates[i].Suggested = model.FieldQuestion
				}
			}
			warnings = append(warnings,
				"No column was clearly named \"Question\". Column "+columnLabel(col)+
					" was chosen because it holds the longest text - confirm or change it.")
		} else {
			// Return the preview anyway: the grid is readable, so the user can
			// pick the Question column from the dropdowns. Blocker keeps the
			// commit disabled until they do.
			return &dto.IngestPreview{
				Grid:       grid,
				HeaderRow:  headerRow,
				Headers:    headerRow2Slice(grid, headerRow),
				Candidates: candidates,
				Mapping:    mapping,
				Warnings:   warnings,
				Blocker:    "Couldn't detect a Question column - please map it manually below.",
			}, nil
		}
	}

	rows, sections, sectionWarnings := DetectSections(grid, mapping, domains)
	warnings = append(warnings, sectionWarnings...)

	questionCount := 0
	for _, r := range rows {
		if r.Kind == dto.RowQuestion {
			questionCount++
		}
	}
	blocker := ""
	if questionCount == 0 {
		blocker = "No question rows were found below the header. Check the Question column is mapped to the right column."
	}

	return &dto.IngestPreview{
		Grid:          grid,
		HeaderRow:     headerRow,
		Headers:       headerRow2Slice(grid, headerRow),
		Blocker:       blocker,
		Candidates:    candidates,
		Mapping:       mapping,
		Rows:          rows,
		Sections:      sections,
		Warnings:      warnings,
		QuestionCount: questionCount,
	}, nil
}

// Apply re-runs row classification against a user-corrected mapping and turns
// the result into model.Question values ready for persistence. domainOverride
// maps a source row index to a domain chosen by the user, which wins over
// whatever the divider detection concluded.
func (p *Parser) Apply(
	grid *dto.Grid,
	mapping *model.ColumnMapping,
	domains []*model.AssessmentDomain,
	domainOverride map[int]uuid.UUID,
	assessmentID uuid.UUID,
) ([]*model.Question, []dto.Section, error) {
	if err := service.ValidateColumnMapping(mapping); err != nil {
		return nil, nil, err
	}
	rows, sections, _ := DetectSections(grid, mapping, domains)

	valid := make(map[uuid.UUID]bool, len(domains))
	for _, d := range domains {
		valid[d.ID] = true
	}

	var (
		questions []*model.Question
		position  int
	)
	for _, row := range rows {
		if row.Kind != dto.RowQuestion {
			continue
		}
		var domainID uuid.UUID
		if row.DomainID != nil {
			domainID = *row.DomainID
		}
		if override, ok := domainOverride[row.Index]; ok && valid[override] {
			domainID = override
		}
		if domainID == uuid.Nil || !valid[domainID] {
			return nil, nil, helper.NewParseError(
				"Row "+itoa(row.Index+1)+" isn't assigned to a domain.",
				"Assign every question a domain in the preview before continuing.", nil)
		}
		q := &model.Question{
			AssessmentID:       assessmentID,
			DomainID:           domainID,
			SourceRow:          row.Index,
			Position:           position,
			QuestionText:       row.Values[model.FieldQuestion],
			AssessorRemark:     row.Values[model.FieldAssessorRemark],
			ThirdPartyAnswer:   row.Values[model.FieldThirdPartyAnswer],
			ThirdPartyRemark:   row.Values[model.FieldThirdPartyRemark],
			ThirdPartyFeedback: row.Values[model.FieldThirdPartyFeedback],
			LinkEvidence:       row.Values[model.FieldLinkEvidence],
			ReviewStatus:       model.ReviewPending,
		}
		// An Assessor Feedback column already present in the file is a prior
		// round's text: seed it as the draft rather than discarding it, but
		// leave review_status pending so it still needs sign-off.
		if prior := row.Values[model.FieldAssessorFeedback]; strings.TrimSpace(prior) != "" {
			q.AssessorFeedbackDraft = prior
		}
		questions = append(questions, q)
		position++
	}
	if len(questions) == 0 {
		return nil, nil, helper.NewParseError("No question rows were found.", "", helper.ErrNoQuestions)
	}
	return questions, sections, nil
}

// headerRow2Slice copies the header row out of the grid.
func headerRow2Slice(grid *dto.Grid, headerRow int) []string {
	headers := make([]string, grid.Width())
	if headerRow >= 0 && headerRow < len(grid.Rows) {
		copy(headers, grid.Rows[headerRow])
	}
	return headers
}

// columnLabel renders a zero-based column index as a spreadsheet letter, so
// error messages say "column C" rather than "column 2".
func columnLabel(i int) string {
	if i < 0 {
		return "?"
	}
	label := ""
	for i >= 0 {
		label = string(rune('A'+i%26)) + label
		i = i/26 - 1
	}
	return label
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var b [20]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		b[pos] = '-'
	}
	return string(b[pos:])
}
