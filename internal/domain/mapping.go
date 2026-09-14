package domain

import (
	"encoding/json"
	"fmt"
	"strings"
)

// QuestionField identifies one logical column of the TPSA questionnaire
// template. The template is a strong prior, not a guarantee, so every field
// except FieldQuestion is optional and any column may be left unmapped.
type QuestionField string

const (
	FieldQuestion           QuestionField = "question_text"
	FieldAssessorRemark     QuestionField = "assessor_remark"
	FieldThirdPartyAnswer   QuestionField = "third_party_answer"
	FieldThirdPartyRemark   QuestionField = "third_party_remark"
	FieldAssessorFeedback   QuestionField = "assessor_feedback"
	FieldThirdPartyFeedback QuestionField = "third_party_feedback"
	FieldLinkEvidence       QuestionField = "link_evidence"
)

// TemplateField describes one expected column, its display label, whether it
// is required, and the header aliases used for fuzzy auto-matching on upload.
type TemplateField struct {
	Field    QuestionField
	Label    string
	Required bool
	Aliases  []string
	Help     string
}

// TemplateFields is the known 7-column template, in template order. Aliases
// are lowercase and whitespace-normalised; matching is fuzzy on top of these.
var TemplateFields = []TemplateField{
	{
		Field: FieldQuestion, Label: "Question", Required: true,
		Aliases: []string{"question", "questions", "control question", "assessment question", "item", "control", "requirement"},
		Help:    "The questionnaire item itself. Required.",
	},
	{
		Field: FieldAssessorRemark, Label: "Assessor Remark", Required: false,
		Aliases: []string{"assessor remark", "assessor remarks", "assessor note", "assessor notes", "internal remark", "reviewer remark", "assessor comment"},
		Help:    "Upfront context or instructions written by the internal assessor.",
	},
	{
		Field: FieldThirdPartyAnswer, Label: "Third Party Answer", Required: false,
		Aliases: []string{"third party answer", "3rd party answer", "vendor answer", "supplier answer", "answer", "response", "third party response", "vendor response"},
		Help:    "The vendor's answer. This is the primary text under AI review.",
	},
	{
		Field: FieldThirdPartyRemark, Label: "Third Party Remark", Required: false,
		Aliases: []string{"third party remark", "3rd party remark", "vendor remark", "vendor remarks", "supplier remark", "third party comment", "vendor comment", "remark"},
		Help:    "Additional remark supplied by the vendor alongside their answer.",
	},
	{
		Field: FieldAssessorFeedback, Label: "Assessor Feedback", Required: false,
		Aliases: []string{"assessor feedback", "reviewer feedback", "assessment feedback", "internal feedback", "feedback from assessor"},
		Help:    "Existing assessor feedback from a previous round, if the file carries any.",
	},
	{
		Field: FieldThirdPartyFeedback, Label: "Third Party Feedback", Required: false,
		Aliases: []string{"third party feedback", "3rd party feedback", "vendor feedback", "supplier feedback", "feedback from third party", "vendor reply"},
		Help:    "Vendor feedback from a later round. Usually empty on a first upload.",
	},
	{
		Field: FieldLinkEvidence, Label: "Link Evidence", Required: false,
		Aliases: []string{"link evidence", "evidence link", "evidence", "link", "links", "supporting evidence", "attachment", "reference", "evidence url"},
		Help:    "Reference to supporting evidence. Stored as text; the link is never fetched.",
	},
}

// TemplateFieldByName looks up a template field definition.
func TemplateFieldByName(f QuestionField) (TemplateField, bool) {
	for _, tf := range TemplateFields {
		if tf.Field == f {
			return tf, true
		}
	}
	return TemplateField{}, false
}

// ColumnBinding records that one spreadsheet column supplies one logical field.
type ColumnBinding struct {
	Field QuestionField `json:"field"`
	// Index is the zero-based column index in the source sheet.
	Index int `json:"index"`
	// Header is the raw header text as it appeared in the file.
	Header string `json:"header"`
	// Confidence is the auto-match score in [0,1]. 1 means an exact alias hit.
	// Zero after a manual override by the user.
	Confidence float64 `json:"confidence"`
	// Manual is true when the user chose this binding rather than the matcher.
	Manual bool `json:"manual"`
}

// ColumnMapping is the confirmed translation from sheet columns to question
// fields, persisted alongside the Assessment for auditability.
type ColumnMapping struct {
	// SheetName is the worksheet the questions were read from (empty for CSV).
	SheetName string `json:"sheet_name,omitempty"`
	// HeaderRow is the zero-based row index the header was found on.
	HeaderRow int `json:"header_row"`
	// Bindings is keyed by logical field. Unmapped fields are absent.
	Bindings map[QuestionField]ColumnBinding `json:"bindings"`
	// IgnoredColumns lists headers present in the file but not mapped.
	IgnoredColumns []string `json:"ignored_columns,omitempty"`
	// ConfirmedAt is set when a human accepted the mapping.
	ConfirmedAt string `json:"confirmed_at,omitempty"`
}

// Index returns the source column index for a field and whether it is mapped.
func (m *ColumnMapping) Index(f QuestionField) (int, bool) {
	if m == nil || m.Bindings == nil {
		return 0, false
	}
	b, ok := m.Bindings[f]
	return b.Index, ok
}

// Validate ensures the mapping is usable: the Question column must be present
// and no two fields may claim the same column.
func (m *ColumnMapping) Validate() error {
	var errs ValidationErrors
	if m == nil || len(m.Bindings) == 0 {
		errs.Add("mapping", "No columns were mapped. Map at least the Question column.")
		return errs.OrNil()
	}
	if _, ok := m.Bindings[FieldQuestion]; !ok {
		errs.Add(string(FieldQuestion), "Couldn't detect a Question column - please map it manually.")
	}
	seen := map[int]QuestionField{}
	for field, b := range m.Bindings {
		if prev, dup := seen[b.Index]; dup {
			errs.Add(string(field), fmt.Sprintf("Column %d is already mapped to %s. Each column may be used once.", b.Index+1, prev))
			continue
		}
		seen[b.Index] = field
		if _, known := TemplateFieldByName(field); !known {
			errs.Add(string(field), "Unknown field "+string(field)+".")
		}
	}
	return errs.OrNil()
}

// MarshalJSON is implemented on the value so the map key type survives a round
// trip through the database as JSONB.
func (m ColumnMapping) MarshalJSON() ([]byte, error) {
	type alias ColumnMapping
	return json.Marshal(alias(m))
}

// NormalizeHeader lowercases, collapses whitespace and strips punctuation
// commonly found in spreadsheet headers so aliases can be compared reliably.
func NormalizeHeader(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	b.Grow(len(s))
	lastSpace := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastSpace = false
		default:
			if !lastSpace && b.Len() > 0 {
				b.WriteByte(' ')
				lastSpace = true
			}
		}
	}
	return strings.TrimSpace(b.String())
}
