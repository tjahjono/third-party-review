package aiclient

import (
	"encoding/json"
	"fmt"
	"strings"

	"third-party-review/internal/dto"
	"third-party-review/internal/helper"
	"third-party-review/internal/model"

	"github.com/google/uuid"
)

// Prompt construction is deliberately provider-neutral: it produces a system
// message and a user message as plain strings, and each concrete client wraps
// them in its own wire format. Nothing here knows about OpenAI or Anthropic.

// maxAnswerRunes caps how much of a single answer is sent. Vendors occasionally
// paste an entire policy into one cell; without a cap one such answer can
// consume the whole context window and starve the rest of the batch.
const (
	maxAnswerRunes = 3000
	maxRemarkRunes = 1200
	maxRubricRunes = 6000
	maxPeerRunes   = 400
)

// systemPrompt is shared by the single and batch review calls.
const systemPrompt = `You are an experienced third-party security assessor reviewing a vendor's completed security questionnaire. You are rigorous, specific and fair. You judge only what the vendor actually wrote.

How to score residual risk, on a 1-5 integer scale:
1 - Strong. A specific, verifiable control is described with scope and cadence.
2 - Adequate. The control is described and plausible; minor detail is missing.
3 - Unclear. The answer is generic or leaves a material question open.
4 - Weak. The answer reveals a gap, or is so vague that the control cannot be relied on.
5 - Serious. The answer describes an absent or broken control, contradicts another answer, or is missing entirely for a material question.

Rules you must follow:
- Judge responsiveness, not length. A short precise answer scores better than a long vague one.
- "Industry standard", "best practice", "as per policy" and similar phrases without specifics are vague, not reassuring.
- A missing or empty answer to a material question is a 5 with completeness "missing".
- An answer about something other than what was asked is completeness "non_responsive".
- Never invent facts about the vendor. If something is not stated, treat it as not stated.
- Evidence links are not opened. Their presence is a mild positive signal; their absence on a claim that needs proof is worth noting but is not on its own a high score.
- When a rubric is supplied, compare against it explicitly and raise a rubric_gap flag for each gap.

The assessor feedback you draft is read by a human who will edit and sign it off. Write it as 2-4 sentences of plain professional prose addressed to the assessment file, not to the vendor. State what was answered, what is missing or weak, and what specifically should be asked for next. Do not use bullet points, headings or markdown.`

// reviewSchema documents the expected JSON shape. It is repeated in the user
// message because models that ignore response_format still follow an inline
// example.
const reviewSchema = `{
  "question_id": <integer, echo the id you were given>,
  "risk_score": <integer 1-5>,
  "completeness": "complete" | "partial" | "missing" | "non_responsive",
  "flags": [
    {"kind": "inconsistency" | "security_risk" | "rubric_gap" | "missing_answer" | "evidence_absent" | "vague",
     "detail": "<one sentence naming the specific problem>",
     "related_question_ids": [<ids of answers this contradicts, omit if none>]}
  ],
  "rationale": "<one or two sentences justifying the score>",
  "assessor_feedback": "<2-4 sentences of prose for the assessment file>",
  "confidence": <number 0.0-1.0, how sure you are of this judgement>
}`

// BuildSingle renders the user message for a one-question review.
func BuildSingle(req dto.ReviewRequest) string {
	var b strings.Builder
	writeRubric(&b, req.RubricExcerpt)
	writePeers(&b, req.PeerAnswers)

	b.WriteString("Review this one questionnaire item.\n\n")
	writeQuestion(&b, req.Question)
	b.WriteString("\nReturn a single JSON object and nothing else, in this shape:\n")
	b.WriteString(reviewSchema)
	b.WriteString("\n")
	return b.String()
}

// BuildBatch renders the user message for a multi-question review. Reviewing a
// domain together is what makes cross-answer contradiction detection possible:
// the model can only notice that two answers conflict if it sees both.
func BuildBatch(req dto.BatchReviewRequest) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Vendor: %s\nAssessment: %s\n\n", orDash(req.VendorName), orDash(req.AssessmentTitle))
	writeRubric(&b, req.RubricExcerpt)
	writePeers(&b, req.PeerAnswers)

	fmt.Fprintf(&b, "Review each of the following %d questionnaire items.\n\n", len(req.Questions))
	for i, q := range req.Questions {
		fmt.Fprintf(&b, "--- ITEM %d ---\n", i+1)
		writeQuestion(&b, q)
		b.WriteString("\n")
	}

	b.WriteString(`Return one JSON object and nothing else:
{
  "results": [ <one object per item, in the shape below> ],
  "notes": [ "<optional observations that span several answers, such as a contradiction between two of them>" ]
}

Each element of "results" must be:
`)
	b.WriteString(reviewSchema)
	b.WriteString("\n\nInclude exactly one result for every item, echoing its question_id. Do not omit an item because it was unanswered - an unanswered item is a finding.\n")
	return b.String()
}

// BuildSummary renders the user message for the assessment-level narrative.
// The aggregate numbers are computed in Go and given to the model as facts, so
// the narrative cannot disagree with the figures shown next to it.
func BuildSummary(req dto.SummaryRequest) string {
	var b strings.Builder
	fmt.Fprintf(&b, `Write the executive summary of a completed third-party security assessment.

Vendor: %s
Assessment: %s
Questions reviewed: %d
Overall residual risk: %.2f out of 5 (%s)
Answers flagged: %d
Answers missing or incomplete: %d

Per-domain residual risk:
`, orDash(req.VendorName), orDash(req.AssessmentTitle), req.QuestionCount,
		req.OverallScore, helper.RiskBandLabel(helper.BandFromFloat(req.OverallScore)),
		req.FlaggedCount, req.IncompleteCount)

	for _, d := range req.DomainScores {
		fmt.Fprintf(&b, "- %s: %.2f/5 (%s), %d question(s), %d flagged, %d incomplete\n",
			d.DomainName, d.WeightedScore, helper.RiskBandLabel(helper.DomainBand(d)), d.QuestionCount, d.FlaggedCount, d.IncompleteCount)
	}

	if len(req.TopFindings) > 0 {
		b.WriteString("\nHighest-risk findings:\n")
		for _, f := range req.TopFindings {
			fmt.Fprintf(&b, "- [%s, score %d, %s] %s\n",
				f.Domain, f.RiskScore, f.Completeness, truncateRunes(f.Question, 200))
			if len(f.Flags) > 0 {
				fmt.Fprintf(&b, "  flags: %s\n", strings.Join(f.Flags, "; "))
			}
		}
	}
	if len(req.BatchNotes) > 0 {
		b.WriteString("\nCross-answer observations:\n")
		for _, n := range req.BatchNotes {
			fmt.Fprintf(&b, "- %s\n", n)
		}
	}

	b.WriteString(`
Write 3 to 5 short paragraphs of plain prose. Open with the overall risk position, then the domains that drive it, then what must be resolved before this vendor is approved. Use the figures given above exactly as stated - do not recompute or contradict them. No bullet points, no headings, no markdown. Return prose only, not JSON.
`)
	return b.String()
}

func writeQuestion(b *strings.Builder, q dto.QuestionContext) {
	fmt.Fprintf(b, "question_id: %s\n", q.QuestionID)
	fmt.Fprintf(b, "Domain: %s\n", orDash(q.DomainName))
	if q.ScrutinyNote != "" {
		fmt.Fprintf(b, "How to judge this domain: %s\n", q.ScrutinyNote)
	}
	fmt.Fprintf(b, "Question: %s\n", truncateRunes(q.QuestionText, maxAnswerRunes))
	if q.AssessorRemark != "" {
		fmt.Fprintf(b, "Assessor's note (internal context, not the vendor's words): %s\n",
			truncateRunes(q.AssessorRemark, maxRemarkRunes))
	}
	answer := strings.TrimSpace(q.ThirdPartyAnswer)
	if answer == "" {
		b.WriteString("Vendor answer: [NO ANSWER PROVIDED]\n")
	} else {
		fmt.Fprintf(b, "Vendor answer: %s\n", truncateRunes(answer, maxAnswerRunes))
	}
	if q.ThirdPartyRemark != "" {
		fmt.Fprintf(b, "Vendor remark: %s\n", truncateRunes(q.ThirdPartyRemark, maxRemarkRunes))
	}
	if q.HasEvidence {
		b.WriteString("Supporting evidence: a reference was supplied (contents not reviewed).\n")
	} else {
		b.WriteString("Supporting evidence: none supplied.\n")
	}
}

func writeRubric(b *strings.Builder, rubric string) {
	if strings.TrimSpace(rubric) == "" {
		return
	}
	b.WriteString("Compare every answer against this policy/rubric. Raise a rubric_gap flag for each requirement the answer fails to meet.\n<rubric>\n")
	b.WriteString(truncateRunes(rubric, maxRubricRunes))
	b.WriteString("\n</rubric>\n\n")
}

func writePeers(b *strings.Builder, peers []dto.PeerAnswer) {
	if len(peers) == 0 {
		return
	}
	b.WriteString("For context, other answers the same vendor gave in this assessment. Use them only to detect contradictions; do not score them here.\n<other_answers>\n")
	for _, p := range peers {
		fmt.Fprintf(b, "[id %d, %s] Q: %s | A: %s\n",
			p.QuestionID, p.Domain,
			truncateRunes(p.Question, maxPeerRunes),
			truncateRunes(orNone(p.Answer), maxPeerRunes))
	}
	b.WriteString("</other_answers>\n\n")
}

// rawResult is the wire shape the model is asked to produce. It is decoded
// leniently: score and confidence accept either a number or a numeric string,
// because smaller models quote numbers unpredictably.
type rawResult struct {
	QuestionID   uuid.UUID  `json:"question_id"`
	RiskScore    flexNumber `json:"risk_score"`
	Completeness string     `json:"completeness"`
	Flags        []rawFlag  `json:"flags"`
	Rationale    string     `json:"rationale"`
	Feedback     string     `json:"assessor_feedback"`
	Confidence   flexNumber `json:"confidence"`
}

type rawFlag struct {
	Kind     string      `json:"kind"`
	Detail   string      `json:"detail"`
	Severity flexNumber  `json:"severity"`
	Related  []uuid.UUID `json:"related_question_ids"`
}

type rawBatch struct {
	Results []rawResult `json:"results"`
	Notes   []string    `json:"notes"`
}

// flexNumber decodes a JSON number that may have been emitted as a string.
type flexNumber float64

func (f *flexNumber) UnmarshalJSON(b []byte) error {
	var n float64
	if err := json.Unmarshal(b, &n); err == nil {
		*f = flexNumber(n)
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		s = strings.TrimSpace(s)
		if s == "" {
			*f = 0
			return nil
		}
		var parsed float64
		if _, err := fmt.Sscanf(s, "%g", &parsed); err == nil {
			*f = flexNumber(parsed)
			return nil
		}
	}
	*f = 0
	return nil
}

// toResult converts a decoded model response into a persistable ReviewResult.
// The model name is a parameter rather than a field so this package's `model`
// import keeps meaning the package.
func (r rawResult) toResult(provider, modelName string) model.ReviewResult {
	out := model.ReviewResult{
		QuestionID:    r.QuestionID,
		RiskScore:     model.RiskScore(int(r.RiskScore + 0.5)),
		Completeness:  model.Completeness(normalizeEnum(r.Completeness)),
		Rationale:     r.Rationale,
		FeedbackDraft: r.Feedback,
		Confidence:    float64(r.Confidence),
		Provider:      provider,
		Model:         modelName,
	}
	for _, f := range r.Flags {
		kind := model.FlagKind(normalizeEnum(f.Kind))
		if kind == "" {
			continue
		}
		out.Flags = append(out.Flags, model.Flag{
			Kind:               kind,
			Detail:             f.Detail,
			Severity:           model.RiskScore(int(f.Severity + 0.5)),
			RelatedQuestionIDs: f.Related,
		})
	}
	helper.NormalizeResult(&out)
	return out
}

// normalizeEnum maps the spellings models actually return onto the canonical
// values, so "Non-Responsive", "non responsive" and "NON_RESPONSIVE" all land
// on the same enum instead of being discarded as unknown.
func normalizeEnum(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.NewReplacer(" ", "_", "-", "_").Replace(s)
	switch s {
	case "nonresponsive", "not_responsive", "unresponsive":
		return "non_responsive"
	case "incomplete", "partially_complete", "partial_answer":
		return "partial"
	case "absent", "empty", "no_answer", "not_answered":
		return "missing"
	case "security_concern", "risk", "security":
		return "security_risk"
	case "inconsistent", "contradiction", "contradictory":
		return "inconsistency"
	case "rubric", "policy_gap", "rubric_violation", "non_compliance":
		return "rubric_gap"
	case "no_evidence", "evidence_missing", "missing_evidence":
		return "evidence_absent"
	case "unclear", "ambiguous", "generic":
		return "vague"
	case "missing_answer_", "no_answer_provided":
		return "missing_answer"
	}
	return s
}

func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + " [truncated]"
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

func orNone(s string) string {
	if strings.TrimSpace(s) == "" {
		return "[no answer]"
	}
	return s
}
