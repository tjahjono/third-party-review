package aiclient

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestExtractJSONFromRealWorldWrappers(t *testing.T) {
	want := `{"risk_score":4,"note":"ok"}`
	cases := map[string]string{
		"bare":              want,
		"markdown fence":    "```json\n" + want + "\n```",
		"unlabelled fence":  "```\n" + want + "\n```",
		"prose preamble":    "Here is my assessment:\n\n" + want,
		"prose both sides":  "Sure!\n" + want + "\nLet me know if you need more.",
		"think tags":        "<think>weighing the answer</think>\n" + want,
		"fence after prose": "Assessment follows.\n```json\n" + want + "\n```\nDone.",
	}
	for name, input := range cases {
		got, err := ExtractJSON(input)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if !json.Valid([]byte(got)) {
			t.Errorf("%s: extracted invalid JSON %q", name, got)
			continue
		}
		var a, b map[string]any
		_ = json.Unmarshal([]byte(got), &a)
		_ = json.Unmarshal([]byte(want), &b)
		if len(a) != len(b) {
			t.Errorf("%s: got %v, want %v", name, a, b)
		}
	}
}

func TestExtractJSONWithBracesInsideStrings(t *testing.T) {
	// A rationale quoting the vendor's answer can contain braces; a naive
	// brace counter truncates the object here.
	input := `{"rationale":"They wrote \"{not applicable}\" with no detail.","risk_score":5}`
	got, err := ExtractJSON(input)
	if err != nil {
		t.Fatalf("ExtractJSON: %v", err)
	}
	var v struct {
		Rationale string `json:"rationale"`
		RiskScore int    `json:"risk_score"`
	}
	if err := json.Unmarshal([]byte(got), &v); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if v.RiskScore != 5 {
		t.Errorf("risk_score = %d, want 5", v.RiskScore)
	}
	if !strings.Contains(v.Rationale, "{not applicable}") {
		t.Errorf("rationale mangled: %q", v.Rationale)
	}
}

func TestExtractJSONArray(t *testing.T) {
	got, err := ExtractJSON("Results:\n[{\"question_id\":1},{\"question_id\":2}]")
	if err != nil {
		t.Fatalf("ExtractJSON: %v", err)
	}
	var arr []map[string]any
	if err := json.Unmarshal([]byte(got), &arr); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(arr) != 2 {
		t.Errorf("got %d elements, want 2", len(arr))
	}
}

func TestExtractJSONRepairsTruncatedOutput(t *testing.T) {
	// Output cut off at the token limit partway through the third result.
	input := `{"results":[{"question_id":1,"risk_score":2},{"question_id":2,"risk_score":4},{"question_id":3,"risk_sc`
	got, err := ExtractJSON(input)
	if err != nil {
		t.Fatalf("truncated output should be salvaged, got %v", err)
	}
	var v struct {
		Results []struct {
			QuestionID int `json:"question_id"`
			RiskScore  int `json:"risk_score"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(got), &v); err != nil {
		t.Fatalf("repaired JSON does not parse: %v (%q)", err, got)
	}
	if len(v.Results) < 2 {
		t.Errorf("salvaged %d complete results, want at least 2", len(v.Results))
	}
}

func TestExtractJSONRejectsNonJSON(t *testing.T) {
	for _, in := range []string{"", "   ", "I cannot help with that request."} {
		if _, err := ExtractJSON(in); err == nil {
			t.Errorf("ExtractJSON(%q) should fail", in)
		}
	}
}

func TestUnmarshalLoose(t *testing.T) {
	var v struct {
		Score int `json:"score"`
	}
	if err := UnmarshalLoose("```json\n{\"score\": 3}\n```", &v); err != nil {
		t.Fatalf("UnmarshalLoose: %v", err)
	}
	if v.Score != 3 {
		t.Errorf("score = %d, want 3", v.Score)
	}
}
