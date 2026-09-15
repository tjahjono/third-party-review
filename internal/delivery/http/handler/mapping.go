package handler

import (
	"net/http"
	"strconv"
	"strings"

	"third-party-review/internal/dto"
	"third-party-review/internal/helper"
	"third-party-review/internal/model"
	"third-party-review/internal/service/parser"

	"github.com/google/uuid"
)

type mappingView struct {
	Assessment *model.Assessment
	Preview    *dto.IngestPreview
	Domains    []*model.AssessmentDomain
	// PreviewRows is the subset of rows shown; a full questionnaire is too
	// long to render in one screen and the user only needs enough to check
	// the boundaries are right.
	PreviewRows []dto.SourceRow
	Truncated   bool
	TotalRows   int
}

// previewRowLimit caps how many rows the confirmation screen renders. Every
// row is still ingested; this is only what the user is shown while checking.
const previewRowLimit = 400

// MappingPage renders the column-mapping and domain-boundary confirmation
// step, pre-filled with the parser's best guesses.
func (h *Handler) MappingPage(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "assessmentID")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	sheet := strings.TrimSpace(r.URL.Query().Get("sheet"))

	a, preview, err := h.assessments.Preview(r.Context(), id, sheet)
	if err != nil {
		// A parse failure still has an assessment to go back to, so it is
		// rendered inside the page rather than as a bare error.
		if a != nil {
			h.renderPage(w, r, http.StatusUnprocessableEntity, "mapping", pageData{
				Title:  "Map columns",
				Active: "assessments",
				Error:  err.Error(),
				Data:   mappingView{Assessment: a},
			})
			return
		}
		h.fail(w, r, err)
		return
	}

	domains, err := h.assessments.ListDomains(r.Context())
	if err != nil {
		h.fail(w, r, err)
		return
	}

	rows := preview.Rows
	truncated := false
	if len(rows) > previewRowLimit {
		rows = rows[:previewRowLimit]
		truncated = true
	}

	h.renderPage(w, r, http.StatusOK, "mapping", pageData{
		Title:   "Map columns - " + a.Title,
		Active:  "assessments",
		Domains: domains,
		Data: mappingView{
			Assessment:  a,
			Preview:     preview,
			Domains:     domains,
			PreviewRows: rows,
			Truncated:   truncated,
			TotalRows:   len(preview.Rows),
		},
	})
}

// RepreviewMapping re-runs detection against the mapping the user is currently
// editing and swaps the preview back, so changing a dropdown updates the
// detected sections immediately rather than after a commit.
func (h *Handler) RepreviewMapping(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "assessmentID")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if err := r.ParseForm(); err != nil {
		h.fail(w, r, helper.ValidationError{Field: "form", Message: "The form could not be read."})
		return
	}

	sheet := strings.TrimSpace(r.FormValue("sheet"))
	a, preview, err := h.assessments.Preview(r.Context(), id, sheet)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	domains, err := h.assessments.ListDomains(r.Context())
	if err != nil {
		h.fail(w, r, err)
		return
	}

	mapping, err := mappingFromForm(r, preview)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	preview.Mapping = mapping
	// The dropdowns render from Candidates, so they have to be brought along
	// or the user's correction is drawn back at its original guess.
	preview.SyncCandidates(mapping)
	preview.Rows, preview.Sections, preview.Warnings = parser.DetectSections(preview.Grid, mapping, domains)
	preview.QuestionCount = countQuestions(preview.Rows)
	preview.Blocker = ""
	if _, ok := mapping.Bindings[model.FieldQuestion]; !ok {
		preview.Blocker = "Couldn't detect a Question column - please map it manually below."
	} else if preview.QuestionCount == 0 {
		preview.Blocker = "No question rows were found below the header. Check the Question column is mapped to the right column."
	}

	rows := preview.Rows
	truncated := false
	if len(rows) > previewRowLimit {
		rows = rows[:previewRowLimit]
		truncated = true
	}

	h.renderPartial(w, r, http.StatusOK, "mapping_preview", mappingView{
		Assessment:  a,
		Preview:     preview,
		Domains:     domains,
		PreviewRows: rows,
		Truncated:   truncated,
		TotalRows:   len(preview.Rows),
	})
}

// ConfirmMapping persists the confirmed mapping and ingests the questions.
func (h *Handler) ConfirmMapping(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "assessmentID")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if err := r.ParseForm(); err != nil {
		h.fail(w, r, helper.ValidationError{Field: "form", Message: "The form could not be read."})
		return
	}

	sheet := strings.TrimSpace(r.FormValue("sheet"))
	_, preview, err := h.assessments.Preview(r.Context(), id, sheet)
	if err != nil {
		h.fail(w, r, err)
		return
	}

	mapping, err := mappingFromForm(r, preview)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	overrides := domainOverridesFromForm(r)

	if _, err := h.assessments.ConfirmMapping(r.Context(), id, mapping, overrides, sheet); err != nil {
		h.fail(w, r, err)
		return
	}
	h.redirect(w, r, "/assessments/"+uuidStr(id))
}

// mappingFromForm reads the per-column dropdowns into a ColumnMapping. The
// form posts one `col_<index>` value per source column naming the field it
// supplies, or "" to ignore the column.
func mappingFromForm(r *http.Request, preview *dto.IngestPreview) (*model.ColumnMapping, error) {
	m := &model.ColumnMapping{
		SheetName: preview.Grid.SheetName,
		HeaderRow: preview.HeaderRow,
		Bindings:  map[model.QuestionField]model.ColumnBinding{},
	}
	if hr := strings.TrimSpace(r.FormValue("header_row")); hr != "" {
		if v, err := strconv.Atoi(hr); err == nil && v >= 0 {
			m.HeaderRow = v
		}
	}

	var errs helper.ValidationErrors
	seen := map[model.QuestionField]int{}

	for i := 0; i < preview.Grid.Width(); i++ {
		raw := strings.TrimSpace(r.FormValue("col_" + strconv.Itoa(i)))
		header := strings.TrimSpace(preview.Grid.Cell(m.HeaderRow, i))
		if raw == "" {
			if header != "" {
				m.IgnoredColumns = append(m.IgnoredColumns, header)
			}
			continue
		}
		field := model.QuestionField(raw)
		if _, known := dto.TemplateFieldByName(field); !known {
			errs.Add("col_"+strconv.Itoa(i), "Unknown field "+raw+".")
			continue
		}
		if prev, dup := seen[field]; dup {
			errs.Add(string(field),
				"Columns "+columnLetterOf(prev)+" and "+columnLetterOf(i)+" are both mapped to "+raw+". Pick one.")
			continue
		}
		seen[field] = i

		confidence := 0.0
		manual := true
		// Carry the auto-match confidence through when the user left the
		// suggestion untouched, so the audit record distinguishes a confirmed
		// guess from a manual correction.
		if b, ok := preview.Mapping.Bindings[field]; ok && b.Index == i {
			confidence, manual = b.Confidence, b.Manual
		}
		m.Bindings[field] = model.ColumnBinding{
			Field: field, Index: i, Header: header,
			Confidence: confidence, Manual: manual,
		}
	}
	if err := errs.OrNil(); err != nil {
		return nil, err
	}
	return m, nil
}

// domainOverridesFromForm reads per-row domain corrections, posted as
// `row_<sourceRowIndex>` with the chosen domain ID.
func domainOverridesFromForm(r *http.Request) map[int]uuid.UUID {
	overrides := map[int]uuid.UUID{}
	for key, values := range r.Form {
		if !strings.HasPrefix(key, "row_") || len(values) == 0 {
			continue
		}
		rowIdx, err := strconv.Atoi(strings.TrimPrefix(key, "row_"))
		if err != nil {
			continue
		}
		domainID, err := uuid.Parse(strings.TrimSpace(values[0]))
		if err != nil || domainID == uuid.Nil {
			continue
		}
		overrides[rowIdx] = domainID
	}
	return overrides
}

func countQuestions(rows []dto.SourceRow) int {
	n := 0
	for _, r := range rows {
		if r.Kind == dto.RowQuestion {
			n++
		}
	}
	return n
}

func columnLetterOf(i int) string {
	label := ""
	for i >= 0 {
		label = string(rune('A'+i%26)) + label
		i = i/26 - 1
	}
	return label
}
