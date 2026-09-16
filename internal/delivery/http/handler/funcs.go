package handler

import (
	"fmt"
	"html/template"
	"strings"
	"time"

	"third-party-review/internal/dto"
	"third-party-review/internal/helper"
	"third-party-review/internal/model"

	"github.com/google/uuid"
)

// templateFuncs are the helpers available to every template. They exist so
// presentation decisions (how a band is coloured, how a date reads) live in
// one place rather than being repeated across templates.
func templateFuncs() template.FuncMap {
	return template.FuncMap{
		"templateFields": func() []dto.TemplateField { return dto.TemplateFields },

		// assessmentStatuses and riskBands give the dashboard a fixed display
		// order; ranging directly over a map would print keys alphabetically
		// instead of in lifecycle/severity order.
		"assessmentStatuses": func() []model.AssessmentStatus { return model.AllAssessmentStatuses },
		"riskBands": func() []model.RiskBand {
			return []model.RiskBand{model.BandCritical, model.BandHigh, model.BandMedium, model.BandLow}
		},

		"fieldLabel": func(f model.QuestionField) string {
			if tf, ok := dto.TemplateFieldByName(f); ok {
				return tf.Label
			}
			return string(f)
		},

		// bandClass maps a risk band to its badge class. Risk bands have their
		// own palette rather than Bootstrap's semantic colours: the brand
		// colour is red, so a "Critical" badge in Bootstrap's danger red would
		// be indistinguishable from a primary button.
		// The model types carry no methods any more, so anything a template
		// used to call as .Status.Label is registered here and delegates to
		// the one implementation in helper. Registering them rather than
		// re-deriving the strings in HTML is what keeps the screen and the CSV
		// export saying the same words.
		"statusLabel":       helper.AssessmentStatusLabel,
		"reviewStatusLabel": helper.ReviewStatusLabel,
		"bandLabel":         helper.RiskBandLabel,
		"completenessLabel": helper.CompletenessLabel,
		"flagLabel":         helper.FlagKindLabel,
		"jobStatusLabel":    helper.JobStatusLabel,

		"resultBand":  helper.ResultBand,
		"domainBand":  helper.DomainBand,
		"summaryBand": helper.SummaryBand,
		"jobPercent":  helper.JobPercent,

		// sameID compares an optional id against a concrete one. A preview row
		// carries *uuid.UUID because it may not have been attributed to a
		// domain yet, and `eq` refuses to compare a pointer with a value.
		"sameID":         func(a *uuid.UUID, b uuid.UUID) bool { return a != nil && *a == b },
		"canStartReview": helper.CanStartReview,

		"bandClass": func(b model.RiskBand) string { return "badge band-" + string(b) },

		// bandColor is the same palette without the badge sizing, for the
		// large score headline which sets its own type scale.
		"bandColor": func(b model.RiskBand) string { return "band-" + string(b) },

		// statusBadge maps an assessment status to a Bootstrap badge class.
		"statusBadge": func(s model.AssessmentStatus) string {
			switch s {
			case model.StatusUploaded:
				return "badge text-bg-light border"
			case model.StatusMapped:
				return "badge text-bg-info"
			case model.StatusReviewing:
				return "badge text-bg-warning"
			case model.StatusReviewed:
				return "badge text-bg-success"
			case model.StatusClosed:
				return "badge text-bg-secondary"
			}
			return "badge text-bg-light border"
		},

		// reviewBadge maps a question's sign-off state to a badge class.
		"reviewBadge": func(s model.ReviewStatus) string {
			switch s {
			case model.ReviewPending:
				return "badge text-bg-light border"
			case model.ReviewAIDrafted:
				return "badge text-bg-warning"
			case model.ReviewFinalized:
				return "badge text-bg-success"
			}
			return "badge text-bg-light border"
		},

		// progressClass colours the AI review progress bar by job state.
		"progressClass": func(s model.JobStatus) string {
			switch s {
			case model.JobSucceeded:
				return "bg-success"
			case model.JobFailed, model.JobCancelled:
				return "bg-danger"
			}
			return ""
		},

		// columnLetter renders a zero-based column index as a spreadsheet
		// letter, which is how a user refers to a column in their own file.
		"columnLetter": func(i int) string {
			if i < 0 {
				return "?"
			}
			label := ""
			for i >= 0 {
				label = string(rune('A'+i%26)) + label
				i = i/26 - 1
			}
			return label
		},

		"pct": func(f float64) string { return fmt.Sprintf("%.0f%%", f*100) },

		"score": func(f float64) string { return fmt.Sprintf("%.2f", f) },

		"date": func(t time.Time) string {
			if t.IsZero() {
				return "-"
			}
			return t.Format("2 Jan 2006, 15:04")
		},

		"dateOrDash": func(t *time.Time) string {
			if t == nil || t.IsZero() {
				return "-"
			}
			return t.Format("2 Jan 2006, 15:04")
		},

		"truncate": func(n int, s string) string {
			r := []rune(s)
			if len(r) <= n {
				return s
			}
			return strings.TrimSpace(string(r[:n])) + "..."
		},

		// paragraphs renders model-written prose as HTML paragraphs. The input
		// is escaped first: it originates from an AI response and must never
		// be trusted as markup.
		"paragraphs": func(s string) template.HTML {
			s = strings.TrimSpace(s)
			if s == "" {
				return ""
			}
			var b strings.Builder
			for _, para := range strings.Split(s, "\n\n") {
				para = strings.TrimSpace(para)
				if para == "" {
					continue
				}
				escaped := template.HTMLEscapeString(para)
				escaped = strings.ReplaceAll(escaped, "\n", "<br>")
				b.WriteString("<p>" + escaped + "</p>")
			}
			return template.HTML(b.String())
		},

		// safeSVG emits an SVG this application generated itself. The only
		// caller is the MFA enrolment QR code, whose markup is built in
		// auth.QRCodeSVG from a secret we created - no user input reaches it.
		// It must never be pointed at anything a user or a model supplied.
		"safeSVG": func(svg string) template.HTML { return template.HTML(svg) },

		"add": func(a, b int) int { return a + b },
		"sub": func(a, b int) int { return a - b },

		"isQuestionRow": func(k dto.RowKind) bool { return k == dto.RowQuestion },
		"isDividerRow":  func(k dto.RowKind) bool { return k == dto.RowDivider },

		"hasEvidence": func(s string) bool { return strings.TrimSpace(s) != "" },

		"defaultStr": func(def, s string) string {
			if strings.TrimSpace(s) == "" {
				return def
			}
			return s
		},

		"dict": func(values ...any) (map[string]any, error) {
			if len(values)%2 != 0 {
				return nil, fmt.Errorf("dict needs an even number of arguments")
			}
			m := make(map[string]any, len(values)/2)
			for i := 0; i < len(values); i += 2 {
				key, ok := values[i].(string)
				if !ok {
					return nil, fmt.Errorf("dict keys must be strings")
				}
				m[key] = values[i+1]
			}
			return m, nil
		},
	}
}
