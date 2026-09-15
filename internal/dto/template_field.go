package dto

import "third-party-review/internal/model"

// TemplateField describes one expected column, its display label, whether it
// is required, and the header aliases used for fuzzy auto-matching on upload.
type TemplateField struct {
	Field    model.QuestionField `json:"field"`
	Label    string              `json:"label"`
	Required bool                `json:"required"`
	Aliases  []string            `json:"aliases"`
	Help     string              `json:"help"`
}

// TemplateFields is the known 7-column template, in template order. Aliases
// are lowercase and whitespace-normalised; matching is fuzzy on top of these.
var TemplateFields = []TemplateField{
	{
		Field: model.FieldQuestion, Label: "Question", Required: true,
		Aliases: []string{"question", "questions", "control question", "assessment question", "item", "control", "requirement"},
		Help:    "The questionnaire item itself. Required.",
	},
	{
		Field: model.FieldAssessorRemark, Label: "Assessor Remark", Required: false,
		Aliases: []string{"assessor remark", "assessor remarks", "assessor note", "assessor notes", "internal remark", "reviewer remark", "assessor comment"},
		Help:    "Upfront context or instructions written by the internal assessor.",
	},
	{
		Field: model.FieldThirdPartyAnswer, Label: "Third Party Answer", Required: false,
		Aliases: []string{"third party answer", "3rd party answer", "vendor answer", "supplier answer", "answer", "response", "third party response", "vendor response"},
		Help:    "The vendor's answer. This is the primary text under AI review.",
	},
	{
		Field: model.FieldThirdPartyRemark, Label: "Third Party Remark", Required: false,
		Aliases: []string{"third party remark", "3rd party remark", "vendor remark", "vendor remarks", "supplier remark", "third party comment", "vendor comment", "remark"},
		Help:    "Additional remark supplied by the vendor alongside their answer.",
	},
	{
		Field: model.FieldAssessorFeedback, Label: "Assessor Feedback", Required: false,
		Aliases: []string{"assessor feedback", "reviewer feedback", "assessment feedback", "internal feedback", "feedback from assessor"},
		Help:    "Existing assessor feedback from a previous round, if the file carries any.",
	},
	{
		Field: model.FieldThirdPartyFeedback, Label: "Third Party Feedback", Required: false,
		Aliases: []string{"third party feedback", "3rd party feedback", "vendor feedback", "supplier feedback", "feedback from third party", "vendor reply"},
		Help:    "Vendor feedback from a later round. Usually empty on a first upload.",
	},
	{
		Field: model.FieldLinkEvidence, Label: "Link Evidence", Required: false,
		Aliases: []string{"link evidence", "evidence link", "evidence", "link", "links", "supporting evidence", "attachment", "reference", "evidence url"},
		Help:    "Reference to supporting evidence. Stored as text; the link is never fetched.",
	},
}

// TemplateFieldByName looks up a template field definition.
func TemplateFieldByName(f model.QuestionField) (TemplateField, bool) {
	for _, tf := range TemplateFields {
		if tf.Field == f {
			return tf, true
		}
	}
	return TemplateField{}, false
}
