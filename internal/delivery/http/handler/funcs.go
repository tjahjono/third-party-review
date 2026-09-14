package handler

import (
	"fmt"
	"html/template"
	"strings"
	"time"

	"third-party-review/internal/domain"
	"third-party-review/internal/service/parser"
)

// templateFuncs are the helpers available to every template. They exist so
// presentation decisions (how a band is coloured, how a date reads) live in
// one place rather than being repeated across templates.
func templateFuncs() template.FuncMap {
	return template.FuncMap{
		"templateFields": func() []domain.TemplateField { return domain.TemplateFields },

		"fieldLabel": func(f domain.QuestionField) string {
			if tf, ok := domain.TemplateFieldByName(f); ok {
				return tf.Label
			}
			return string(f)
		},

		// bandClass maps a risk band to its badge class. Risk bands have their
		// own palette rather than Bootstrap's semantic colours: the brand
		// colour is red, so a "Critical" badge in Bootstrap's danger red would
		// be indistinguishable from a primary button.
		"bandClass": func(b domain.RiskBand) string { return "badge band-" + string(b) },

		// bandColor is the same palette without the badge sizing, for the
		// large score headline which sets its own type scale.
		"bandColor": func(b domain.RiskBand) string { return "band-" + string(b) },

		// statusBadge maps an assessment status to a Bootstrap badge class.
		"statusBadge": func(s domain.AssessmentStatus) string {
			switch s {
			case domain.StatusUploaded:
				return "badge text-bg-light border"
			case domain.StatusMapped:
				return "badge text-bg-info"
			case domain.StatusReviewing:
				return "badge text-bg-warning"
			case domain.StatusReviewed:
				return "badge text-bg-success"
			case domain.StatusClosed:
				return "badge text-bg-secondary"
			}
			return "badge text-bg-light border"
		},

		// reviewBadge maps a question's sign-off state to a badge class.
		"reviewBadge": func(s domain.ReviewStatus) string {
			switch s {
			case domain.ReviewPending:
				return "badge text-bg-light border"
			case domain.ReviewAIDrafted:
				return "badge text-bg-warning"
			case domain.ReviewFinalized:
				return "badge text-bg-success"
			}
			return "badge text-bg-light border"
		},

		// progressClass colours the AI review progress bar by job state.
		"progressClass": func(s domain.JobStatus) string {
			switch s {
			case domain.JobSucceeded:
				return "bg-success"
			case domain.JobFailed, domain.JobCancelled:
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

		"isQuestionRow": func(k parser.RowKind) bool { return k == parser.RowQuestion },
		"isDividerRow":  func(k parser.RowKind) bool { return k == parser.RowDivider },

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
