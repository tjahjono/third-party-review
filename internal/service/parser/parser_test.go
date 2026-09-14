package parser

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"third-party-review/internal/domain"
)

func TestParseStandardTemplate(t *testing.T) {
	data := buildWorkbook(t, "Assessment", standardRows(), 4, 8, 11)
	p := New()

	pv, err := p.Parse(bytes.NewReader(data), "tpsa.xlsx", seededDomains())
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if pv.HeaderRow != 3 {
		t.Errorf("HeaderRow = %d, want 3 (the title block sits above it)", pv.HeaderRow)
	}
	// All 7 template columns should auto-map from the canonical header row.
	for _, tf := range domain.TemplateFields {
		b, ok := pv.Mapping.Bindings[tf.Field]
		if !ok {
			t.Errorf("field %s was not auto-mapped", tf.Field)
			continue
		}
		if b.Confidence < autoMapThreshold {
			t.Errorf("field %s mapped with confidence %.2f, want >= %.2f", tf.Field, b.Confidence, autoMapThreshold)
		}
	}
	if err := pv.Mapping.Validate(); err != nil {
		t.Errorf("auto mapping is invalid: %v", err)
	}

	if pv.QuestionCount != 5 {
		t.Errorf("QuestionCount = %d, want 5", pv.QuestionCount)
	}
	if len(pv.Sections) != 3 {
		t.Fatalf("detected %d sections, want 3: %+v", len(pv.Sections), pv.Sections)
	}
	wantDomains := []string{"Network Security", "Cloud Security", "AI Security"}
	for i, want := range wantDomains {
		if pv.Sections[i].DomainName != want {
			t.Errorf("section %d = %q, want %q", i, pv.Sections[i].DomainName, want)
		}
		if pv.Sections[i].Inferred {
			t.Errorf("section %d (%s) was inferred, but a divider exists for it", i, want)
		}
	}
	if got := pv.Sections[0].RowCount; got != 2 {
		t.Errorf("Network Security row count = %d, want 2", got)
	}
}

func TestApplyProducesQuestionsWithDomains(t *testing.T) {
	data := buildWorkbook(t, "Assessment", standardRows(), 4, 8, 11)
	p := New()
	domains := seededDomains()

	pv, err := p.Parse(bytes.NewReader(data), "tpsa.xlsx", domains)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	questions, _, err := p.Apply(pv.Grid, pv.Mapping, domains, nil, 42)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(questions) != 5 {
		t.Fatalf("got %d questions, want 5", len(questions))
	}

	for i, q := range questions {
		if q.AssessmentID != 42 {
			t.Errorf("question %d AssessmentID = %d, want 42", i, q.AssessmentID)
		}
		// Every question must end up associated with exactly one domain.
		if q.DomainID == 0 {
			t.Errorf("question %d (%q) has no domain", i, q.QuestionText)
		}
		if q.Position != i {
			t.Errorf("question %d Position = %d, want %d", i, q.Position, i)
		}
		if q.ReviewStatus != domain.ReviewPending {
			t.Errorf("question %d ReviewStatus = %s, want pending", i, q.ReviewStatus)
		}
	}

	first := questions[0]
	if !strings.Contains(first.QuestionText, "segment your production network") {
		t.Errorf("first question text = %q", first.QuestionText)
	}
	if !strings.Contains(first.ThirdPartyAnswer, "isolated VLANs") {
		t.Errorf("first answer = %q", first.ThirdPartyAnswer)
	}
	if !first.HasEvidence() {
		t.Error("first question should report evidence present")
	}
	if first.AssessorRemark == "" {
		t.Error("assessor remark was dropped")
	}
	// The unanswered cloud-posture question must survive ingestion so the AI
	// can flag it as missing rather than it vanishing silently.
	var blank *domain.Question
	for _, q := range questions {
		if strings.Contains(q.QuestionText, "posture") {
			blank = q
		}
	}
	if blank == nil {
		t.Fatal("the unanswered question was dropped during ingestion")
	}
	if !blank.AnswerIsBlank() {
		t.Error("unanswered question should report a blank answer")
	}
}

func TestApplyDomainOverrideWins(t *testing.T) {
	data := buildWorkbook(t, "Assessment", standardRows(), 4, 8, 11)
	p := New()
	domains := seededDomains()
	pv, err := p.Parse(bytes.NewReader(data), "tpsa.xlsx", domains)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	// The user corrects a misdetected boundary: move row 6 (the firewall
	// question, detected as Network Security) into Data Security.
	var dataSecurityID int64
	for _, d := range domains {
		if d.Name == "Data Security" {
			dataSecurityID = d.ID
		}
	}
	questions, _, err := p.Apply(pv.Grid, pv.Mapping, domains, map[int]int64{6: dataSecurityID}, 1)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	for _, q := range questions {
		if q.SourceRow == 6 && q.DomainID != dataSecurityID {
			t.Errorf("override ignored: row 6 domain = %d, want %d", q.DomainID, dataSecurityID)
		}
	}
}

func TestParseCSVWithoutFeedbackColumns(t *testing.T) {
	// A first-round file: no Third Party Feedback column at all, headers in a
	// different case, and a quoted multi-line answer.
	csv := "QUESTION,assessor remarks,3rd Party Answer,Link Evidence\n" +
		"1. Network Security,,,\n" +
		"\"Do you segment networks?\",Check VLANs,\"Yes.\nReviewed quarterly.\",https://e.example/1\n" +
		"7. Cloud Security,,,\n" +
		"\"Where is data hosted?\",,AWS eu-west-1,\n"

	p := New()
	pv, err := p.Parse(strings.NewReader(csv), "round1.csv", seededDomains())
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if _, ok := pv.Mapping.Bindings[domain.FieldThirdPartyFeedback]; ok {
		t.Error("Third Party Feedback should be unmapped when the column is absent")
	}
	if _, ok := pv.Mapping.Bindings[domain.FieldQuestion]; !ok {
		t.Fatal("Question column was not mapped despite uppercase header")
	}
	if _, ok := pv.Mapping.Bindings[domain.FieldThirdPartyAnswer]; !ok {
		t.Fatal(`"3rd Party Answer" was not mapped to third_party_answer`)
	}
	if pv.QuestionCount != 2 {
		t.Fatalf("QuestionCount = %d, want 2", pv.QuestionCount)
	}
	if len(pv.Sections) != 2 {
		t.Fatalf("got %d sections, want 2", len(pv.Sections))
	}

	questions, _, err := p.Apply(pv.Grid, pv.Mapping, seededDomains(), nil, 1)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !strings.Contains(questions[0].ThirdPartyAnswer, "\n") {
		t.Errorf("multi-line answer was flattened: %q", questions[0].ThirdPartyAnswer)
	}
}

func TestPreviewIsStillUsableWithoutAQuestionColumn(t *testing.T) {
	// Nothing resembling a question: short codes only, so even the
	// content-based fallback declines. The file is still readable, so the user
	// must get the mapping UI rather than a dead end - telling someone to map
	// a column manually and then not showing them the columns is not an error
	// they can act on.
	csv := "Ref,Status,Owner\nA1,Open,Ops\nA2,Closed,Sec\n"
	pv, err := New().Parse(strings.NewReader(csv), "junk.csv", seededDomains())
	if err != nil {
		t.Fatalf("a readable file should preview, not fail: %v", err)
	}
	if pv.Ingestable() {
		t.Error("a preview with no Question column must not be ingestable")
	}
	if !strings.Contains(pv.Blocker, "Question column") {
		t.Errorf("blocker should name the missing column, got %q", pv.Blocker)
	}
	if len(pv.Candidates) != 3 {
		t.Errorf("got %d column candidates, want 3 so every column can be mapped by hand", len(pv.Candidates))
	}

	// Committing is still refused until the column is chosen.
	if _, _, err := New().Apply(pv.Grid, pv.Mapping, seededDomains(), nil, 1); err == nil {
		t.Error("Apply should refuse a mapping with no Question column")
	} else if !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("Apply error should map to invalid input, got %v", err)
	}
}

func TestFallbackQuestionColumnOnUnnamedHeaders(t *testing.T) {
	// Headers are opaque, but one column clearly holds questions.
	csv := "Col A,Col B,Col C\n" +
		"REF-1,Do you encrypt customer data at rest and in transit?,Yes AES-256\n" +
		"REF-2,Are encryption keys rotated on a defined schedule?,Annually\n" +
		"REF-3,Is key custody separated from data custody?,Yes\n"

	pv, err := New().Parse(strings.NewReader(csv), "odd.csv", seededDomains())
	if err != nil {
		t.Fatalf("Parse should fall back rather than fail: %v", err)
	}
	b, ok := pv.Mapping.Bindings[domain.FieldQuestion]
	if !ok {
		t.Fatal("fallback did not map a question column")
	}
	if b.Index != 1 {
		t.Errorf("fallback chose column %d, want 1 (the prose column)", b.Index)
	}
	if b.Confidence != 0 {
		t.Errorf("a fallback guess should carry zero confidence, got %.2f", b.Confidence)
	}
	if len(pv.Warnings) == 0 {
		t.Error("a fallback guess must warn the user to confirm it")
	}
}

func TestQuestionMentioningDomainIsNotADivider(t *testing.T) {
	// The trap case: a question whose text names a domain. Because the row has
	// question text it must stay a question, not open a new section.
	rows := [][]string{
		templateHeaders,
		{"1. Network Security", "", "", "", "", "", ""},
		{"Describe how Cloud Security responsibilities are split with your provider.", "",
			"Shared responsibility model documented.", "", "", "", ""},
		{"Do you monitor perimeter traffic?", "", "Yes.", "", "", "", ""},
	}
	data := buildWorkbook(t, "Sheet1", rows, 1)

	pv, err := New().Parse(bytes.NewReader(data), "trap.xlsx", seededDomains())
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(pv.Sections) != 1 {
		t.Fatalf("got %d sections, want 1 - a question mentioning a domain opened a false section", len(pv.Sections))
	}
	if pv.Sections[0].DomainName != "Network Security" {
		t.Errorf("section = %q, want Network Security", pv.Sections[0].DomainName)
	}
	if pv.Sections[0].RowCount != 2 {
		t.Errorf("row count = %d, want 2 (both questions stay in Network Security)", pv.Sections[0].RowCount)
	}
}

func TestQuestionsBeforeFirstDividerAreAttributedAndFlagged(t *testing.T) {
	rows := [][]string{
		templateHeaders,
		{"Do you maintain an asset inventory?", "", "Yes.", "", "", "", ""},
		{"1. Network Security", "", "", "", "", "", ""},
		{"Do you segment networks?", "", "Yes.", "", "", "", ""},
	}
	data := buildWorkbook(t, "Sheet1", rows, 2)

	pv, err := New().Parse(bytes.NewReader(data), "early.xlsx", seededDomains())
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(pv.Sections) != 2 {
		t.Fatalf("got %d sections, want 2 (one inferred, one detected)", len(pv.Sections))
	}
	if !pv.Sections[0].Inferred {
		t.Error("the leading block should be marked inferred so the user checks it")
	}
	var warned bool
	for _, w := range pv.Warnings {
		if strings.Contains(w, "before the first domain heading") {
			warned = true
		}
	}
	if !warned {
		t.Errorf("expected a warning about rows before the first heading, got %v", pv.Warnings)
	}

	// Critically, no question may be silently dropped.
	questions, _, err := New().Apply(pv.Grid, pv.Mapping, seededDomains(), nil, 1)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(questions) != 2 {
		t.Fatalf("got %d questions, want 2", len(questions))
	}
	for _, q := range questions {
		if q.DomainID == 0 {
			t.Errorf("question %q left without a domain", q.QuestionText)
		}
	}
}

func TestMergedDividerCellIsDetected(t *testing.T) {
	// excelize reports a merged region's value only in its top-left cell. If
	// expansion is wrong the divider row reads as blank and every question
	// below it loses its domain.
	rows := [][]string{
		templateHeaders,
		{"4. Data Security", "", "", "", "", "", ""},
		{"Is data encrypted at rest?", "", "Yes, AES-256.", "", "", "", ""},
	}
	data := buildWorkbook(t, "Sheet1", rows, 1)

	pv, err := New().Parse(bytes.NewReader(data), "merged.xlsx", seededDomains())
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(pv.Sections) != 1 || pv.Sections[0].DomainName != "Data Security" {
		t.Fatalf("merged divider not detected: sections = %+v", pv.Sections)
	}
	if pv.Sections[0].Inferred {
		t.Error("a real merged divider should not produce an inferred section")
	}
}

func TestDividerVariantsAndPunctuation(t *testing.T) {
	cases := []struct {
		divider string
		want    string
	}{
		{"1. Network Security", "Network Security"},
		{"2) Application Security", "Application Security"},
		{"Section 3 - Logical Access Security", "Logical Access Security"},
		{"DATA SECURITY", "Data Security"},
		{"5. Security Logging & Monitoring", "Security Logging and Monitoring"},
		{"6. Change, Performance and Capacity Management", "Change, Performance and Capacity Management"},
		{"  7.  Cloud   Security  ", "Cloud Security"},
		{"8. AI Security", "AI Security"},
	}
	for _, c := range cases {
		rows := [][]string{
			templateHeaders,
			{c.divider, "", "", "", "", "", ""},
			{"Is there a documented control?", "", "Yes.", "", "", "", ""},
		}
		data := buildWorkbook(t, "Sheet1", rows, 1)
		pv, err := New().Parse(bytes.NewReader(data), "d.xlsx", seededDomains())
		if err != nil {
			t.Errorf("divider %q: parse failed: %v", c.divider, err)
			continue
		}
		if len(pv.Sections) != 1 {
			t.Errorf("divider %q: got %d sections, want 1", c.divider, len(pv.Sections))
			continue
		}
		if pv.Sections[0].DomainName != c.want {
			t.Errorf("divider %q resolved to %q, want %q", c.divider, pv.Sections[0].DomainName, c.want)
		}
	}
}

func TestBlankAndRaggedRowsAreTolerated(t *testing.T) {
	// Ragged rows (short records) plus stray blank lines are normal exports.
	csv := "Question,Third Party Answer\n" +
		"1. Network Security\n" +
		"\n" +
		"Do you segment networks?,Yes\n" +
		",\n" +
		"Do you log firewall changes?,Yes\n"

	pv, err := New().Parse(strings.NewReader(csv), "ragged.csv", seededDomains())
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if pv.QuestionCount != 2 {
		t.Errorf("QuestionCount = %d, want 2", pv.QuestionCount)
	}
}

func TestSemicolonDelimitedCSV(t *testing.T) {
	csv := "Question;Third Party Answer;Link Evidence\n" +
		"1. Network Security;;\n" +
		"Do you segment networks?;Yes, on isolated VLANs;https://e.example/1\n"

	pv, err := New().Parse(strings.NewReader(csv), "eu.csv", seededDomains())
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if pv.QuestionCount != 1 {
		t.Fatalf("QuestionCount = %d, want 1", pv.QuestionCount)
	}
	questions, _, err := New().Apply(pv.Grid, pv.Mapping, seededDomains(), nil, 1)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	// The comma inside the answer must not have been treated as a delimiter.
	if questions[0].ThirdPartyAnswer != "Yes, on isolated VLANs" {
		t.Errorf("answer = %q, want the full comma-containing text", questions[0].ThirdPartyAnswer)
	}
}

func TestEmptyAndGarbageFiles(t *testing.T) {
	p := New()
	if _, err := p.Parse(strings.NewReader(""), "empty.csv", seededDomains()); err == nil {
		t.Error("empty file should fail")
	}
	if _, err := p.Parse(bytes.NewReader([]byte{0xFF, 0xFE, 0x00, 0x01}), "bin.dat", seededDomains()); err == nil {
		t.Error("binary garbage should fail")
	}
	// Every failure must be user-facing, never a bare internal error.
	_, err := p.Parse(strings.NewReader(""), "empty.csv", seededDomains())
	var pe *ParseError
	if !errors.As(err, &pe) {
		t.Fatalf("expected *ParseError, got %T", err)
	}
}

func TestMappingValidateRejectsDuplicateColumns(t *testing.T) {
	m := &domain.ColumnMapping{Bindings: map[domain.QuestionField]domain.ColumnBinding{
		domain.FieldQuestion:         {Field: domain.FieldQuestion, Index: 0},
		domain.FieldThirdPartyAnswer: {Field: domain.FieldThirdPartyAnswer, Index: 0},
	}}
	err := m.Validate()
	if err == nil {
		t.Fatal("two fields sharing one column should be rejected")
	}
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("error should map to invalid input, got %v", err)
	}
}

func TestColumnLabel(t *testing.T) {
	cases := map[int]string{0: "A", 1: "B", 25: "Z", 26: "AA", 27: "AB", 51: "AZ", 52: "BA"}
	for in, want := range cases {
		if got := columnLabel(in); got != want {
			t.Errorf("columnLabel(%d) = %q, want %q", in, got, want)
		}
	}
}
