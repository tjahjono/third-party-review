package domain

import (
	"errors"
	"fmt"
)

// The ingestion model lives here rather than in the parser because it is data,
// not algorithm: the service layer produces it, the delivery layer renders it,
// and the IngestService contract below names it. A parser-owned type could not
// appear in a domain interface without domain importing a service package,
// which would invert the dependency direction the whole layout exists to keep.
//
// The parser keeps the logic - reading spreadsheets, matching headers,
// detecting section boundaries - and fills these in.

// Grid is a raw rectangular view of one worksheet or delimited file. Rows are
// right-padded to a common width by the readers, so indexing is always safe.
type Grid struct {
	// SheetName is the worksheet the grid came from; empty for CSV.
	SheetName string
	// SheetNames lists every worksheet in the workbook, so the UI can offer a
	// different one when the wrong sheet was auto-selected.
	SheetNames []string
	Rows       [][]string
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
	Index int
	Kind  RowKind
	Cells []string

	// DomainID is the domain this row was assigned to. Zero on divider, blank
	// and header rows, and on question rows falling before any detected
	// divider.
	DomainID   int64
	DomainName string

	// DividerText is the raw text of a divider row, e.g. "1. Network Security".
	DividerText string
	// DividerScore is the fuzzy match score that identified the divider.
	DividerScore float64

	// Values holds the mapped field values for a question row.
	Values map[QuestionField]string
}

// QuestionText is a convenience accessor used by the preview template.
func (r SourceRow) QuestionText() string { return r.Values[FieldQuestion] }

// Answer is a convenience accessor used by the preview template.
func (r SourceRow) Answer() string { return r.Values[FieldThirdPartyAnswer] }

// Section is a contiguous block of question rows under one domain.
type Section struct {
	DomainID   int64
	DomainName string
	// DividerRow is the index of the divider that opened the section, or -1
	// when the section was inferred rather than found.
	DividerRow int
	FirstRow   int
	LastRow    int
	RowCount   int
	// Inferred is true when no divider was found for this block and the rows
	// were attributed by fallback rather than detection. The UI highlights
	// these so the user knows which boundaries to check.
	Inferred bool
}

// HeaderCandidate describes one source column and its best template match.
type HeaderCandidate struct {
	Index  int
	Header string
	// Suggested is the template field the matcher picked, or "" if none
	// cleared the threshold.
	Suggested  QuestionField
	Confidence float64
}

// IngestPreview is the complete result of parsing a file: everything the user
// needs to confirm or correct before anything is written to the database.
type IngestPreview struct {
	Grid *Grid

	HeaderRow  int
	Headers    []string
	Candidates []HeaderCandidate

	// Mapping is the best-guess column mapping, pre-filled for the UI.
	Mapping *ColumnMapping

	Rows     []SourceRow
	Sections []Section

	// Warnings are non-fatal observations worth showing the user, such as a
	// domain that matched weakly or rows that landed outside any section.
	Warnings []string

	// Blocker, when set, is the one thing standing between this preview and a
	// usable ingest - in practice always an unidentifiable Question column.
	// The preview is still returned so the user can fix it on the mapping
	// screen; telling someone to "map it manually" and then giving them no
	// mapping UI is not a usable error.
	Blocker string

	QuestionCount int
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
func (p *IngestPreview) SyncCandidates(m *ColumnMapping) {
	if p == nil || m == nil {
		return
	}
	byColumn := make(map[int]ColumnBinding, len(m.Bindings))
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

// ErrNoQuestions is returned when a file parses cleanly but yields no usable
// question rows.
var ErrNoQuestions = errors.New("no question rows found")

// ParseError is a user-facing ingestion failure. Message is written for a
// security assessor, not a developer, because it is rendered straight into an
// HTMX partial.
type ParseError struct {
	Message string
	// Hint suggests the next action, e.g. which column to map manually.
	Hint string
	Err  error
}

func (e *ParseError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %v", e.Message, e.Err)
	}
	return e.Message
}

func (e *ParseError) Unwrap() error { return e.Err }

// Is lets errors.Is(err, ErrInvalidInput) succeed for any ParseError, so
// handlers map them to a 422 partial rather than a 500.
func (e *ParseError) Is(target error) bool { return target == ErrInvalidInput }

// NewParseError builds a user-facing ingestion failure.
func NewParseError(message, hint string, err error) *ParseError {
	return &ParseError{Message: message, Hint: hint, Err: err}
}
