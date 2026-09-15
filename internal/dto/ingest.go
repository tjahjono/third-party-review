package dto

import (
	"github.com/google/uuid"

	"third-party-review/internal/model"
)

// The ingestion model lives here rather than in the parser because it is data,
// not algorithm: the service layer produces it, the delivery layer renders it,
// and the IngestService contract names it. A parser-owned type could not
// appear in a service contract without inverting the dependency direction the
// whole layout exists to keep.
//
// The parser keeps the logic - reading spreadsheets, matching headers,
// detecting section boundaries - and fills these in.

// Grid is a raw rectangular view of one worksheet or delimited file. Rows are
// right-padded to a common width by the readers, so indexing is always safe.
type Grid struct {
	// SheetName is the worksheet the grid came from; empty for CSV.
	SheetName string `json:"sheet_name,omitempty"`
	// SheetNames lists every worksheet in the workbook, so the UI can offer a
	// different one when the wrong sheet was auto-selected.
	SheetNames []string   `json:"sheet_names,omitempty"`
	Rows       [][]string `json:"rows"`
}

// Width returns the number of columns, which is uniform across rows.
func (g *Grid) Width() int {
	if g == nil || len(g.Rows) == 0 {
		return 0
	}
	return len(g.Rows[0])
}

// Cell returns the cell at (row, col), or "" when out of range.
func (g *Grid) Cell(row, col int) string {
	if g == nil || row < 0 || row >= len(g.Rows) {
		return ""
	}
	if col < 0 || col >= len(g.Rows[row]) {
		return ""
	}
	return g.Rows[row][col]
}

// RowKind classifies a source row during domain detection.
type RowKind string

const (
	// RowQuestion is a real questionnaire item.
	RowQuestion RowKind = "question"
	// RowDivider names a domain section and carries no question.
	RowDivider RowKind = "divider"
	// RowBlank is empty or structural filler.
	RowBlank RowKind = "blank"
	// RowHeader is the detected header row.
	RowHeader RowKind = "header"
)

// SourceRow is one classified row in the ingestion preview.
type SourceRow struct {
	// Index is the zero-based row index in the source grid.
	Index int      `json:"index"`
	Kind  RowKind  `json:"kind"`
	Cells []string `json:"cells"`

	// DomainID is the domain this row was assigned to. Nil on divider, blank
	// and header rows, and on question rows falling before any detected
	// divider.
	DomainID   *uuid.UUID `json:"domain_id,omitempty"`
	DomainName string     `json:"domain_name,omitempty"`

	// DividerText is the raw text of a divider row, e.g. "1. Network Security".
	DividerText string `json:"divider_text,omitempty"`
	// DividerScore is the fuzzy match score that identified the divider.
	DividerScore float64 `json:"divider_score,omitempty"`

	// Values holds the mapped field values for a question row.
	Values map[model.QuestionField]string `json:"values,omitempty"`
}

// QuestionText is a convenience accessor used by the preview template.
func (r SourceRow) QuestionText() string { return r.Values[model.FieldQuestion] }

// Answer is a convenience accessor used by the preview template.
func (r SourceRow) Answer() string { return r.Values[model.FieldThirdPartyAnswer] }

// HasDomain reports whether the row was attributed to a domain, so templates
// can test it without dereferencing a nil pointer.
func (r SourceRow) HasDomain() bool { return r.DomainID != nil && *r.DomainID != uuid.Nil }

// Section is a contiguous block of question rows under one domain.
type Section struct {
	DomainID   uuid.UUID `json:"domain_id"`
	DomainName string    `json:"domain_name"`
	// DividerRow is the index of the divider that opened the section, or -1
	// when the section was inferred rather than found.
	DividerRow int `json:"divider_row"`
	FirstRow   int `json:"first_row"`
	LastRow    int `json:"last_row"`
	RowCount   int `json:"row_count"`
	// Inferred is true when no divider was found for this block and the rows
	// were attributed by fallback rather than detection. The UI highlights
	// these so the user knows which boundaries to check.
	Inferred bool `json:"inferred"`
}

// HeaderCandidate describes one source column and its best template match.
type HeaderCandidate struct {
	Index  int    `json:"index"`
	Header string `json:"header"`
	// Suggested is the template field the matcher picked, or "" if none
	// cleared the threshold.
	Suggested  model.QuestionField `json:"suggested,omitempty"`
	Confidence float64             `json:"confidence"`
}

// IngestPreview is the complete result of parsing a file: everything the user
// needs to confirm or correct before anything is written to the database.
type IngestPreview struct {
	Grid *Grid `json:"grid,omitempty"`

	HeaderRow  int               `json:"header_row"`
	Headers    []string          `json:"headers"`
	Candidates []HeaderCandidate `json:"candidates"`

	// Mapping is the best-guess column mapping, pre-filled for the UI.
	Mapping *model.ColumnMapping `json:"mapping,omitempty"`

	Rows     []SourceRow `json:"rows"`
	Sections []Section   `json:"sections"`

	// Warnings are non-fatal observations worth showing the user, such as a
	// domain that matched weakly or rows that landed outside any section.
	Warnings []string `json:"warnings,omitempty"`

	// Blocker, when set, is the one thing standing between this preview and a
	// usable ingest - in practice always an unidentifiable Question column.
	// The preview is still returned so the user can fix it on the mapping
	// screen; telling someone to "map it manually" and then giving them no
	// mapping UI is not a usable error.
	Blocker string `json:"blocker,omitempty"`

	QuestionCount int `json:"question_count"`
}

// Ingestable reports whether the preview can be committed as it stands.
func (p *IngestPreview) Ingestable() bool {
	return p != nil && p.Blocker == "" && p.QuestionCount > 0
}

// SyncCandidates rewrites the per-column candidate list to agree with a
// mapping.
//
// Candidates is what the mapping screen renders its dropdowns from, while
// Mapping is what the parser actually uses. They are produced together by the
// auto-matcher, but anything that replaces Mapping afterwards - a user
// correcting a column, or a previously confirmed mapping being restored - must
// bring Candidates with it. Without this the screen redraws every dropdown at
// its original guess and the correction silently disappears, which looks from
// the outside like the control does nothing at all.
func (p *IngestPreview) SyncCandidates(m *model.ColumnMapping) {
	if p == nil || m == nil {
		return
	}
	byColumn := make(map[int]model.ColumnBinding, len(m.Bindings))
	for _, b := range m.Bindings {
		byColumn[b.Index] = b
	}
	for i := range p.Candidates {
		b, mapped := byColumn[p.Candidates[i].Index]
		if !mapped {
			p.Candidates[i].Suggested = ""
			p.Candidates[i].Confidence = 0
			continue
		}
		p.Candidates[i].Suggested = b.Field
		p.Candidates[i].Confidence = b.Confidence
	}
}
