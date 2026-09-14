package fuzzy

import "testing"

func TestRatioExactAndEmpty(t *testing.T) {
	if got := Ratio("Question", "question"); got != 1 {
		t.Fatalf("case-insensitive exact match = %v, want 1", got)
	}
	if got := Ratio("", "question"); got != 0 {
		t.Fatalf("empty input = %v, want 0", got)
	}
}

func TestRatioRealHeaderVariants(t *testing.T) {
	// Each pair is a header spelling seen in the wild against its canonical
	// alias. All must clear the 0.7 auto-map threshold used by the parser.
	pairs := [][2]string{
		{"assessor remarks", "assessor remark"},
		{"3rd party answer", "third party answer"},
		{"link evidence", "evidence link"},
		{"third  party   remark", "third party remark"},
	}
	for _, p := range pairs {
		if got := Ratio(p[0], p[1]); got < 0.7 {
			t.Errorf("Ratio(%q,%q) = %.3f, want >= 0.7", p[0], p[1], got)
		}
	}
}

// Synonyms that share no words ("vendor response" vs "vendor answer") are
// deliberately not this matcher's job - the parser carries an alias list per
// template field for those. This test pins that expectation so nobody "fixes"
// the matcher by lowering its threshold.
func TestRatioDoesNotResolveSynonyms(t *testing.T) {
	if got := Ratio("vendor response", "vendor answer"); got >= 0.7 {
		t.Errorf("Ratio(vendor response, vendor answer) = %.3f; synonyms belong in the alias list, not the matcher", got)
	}
}

func TestRatioRejectsUnrelated(t *testing.T) {
	pairs := [][2]string{
		{"question", "link evidence"},
		{"assessor remark", "third party answer"},
		{"cloud security", "network security"},
	}
	for _, p := range pairs {
		if got := Ratio(p[0], p[1]); got >= 0.7 {
			t.Errorf("Ratio(%q,%q) = %.3f, want < 0.7 (false positive)", p[0], p[1], got)
		}
	}
}

func TestContains(t *testing.T) {
	if got := Contains("question (control statement)", "question"); got < 0.75 {
		t.Errorf("Contains = %.3f, want >= 0.75", got)
	}
	if got := Contains("answer", "question"); got != 0 {
		t.Errorf("Contains for non-substring = %v, want 0", got)
	}
}

func TestBestMatch(t *testing.T) {
	candidates := []string{"question", "assessor remark", "third party answer"}
	idx, score := BestMatch("3rd party answer", candidates)
	if idx != 2 {
		t.Fatalf("BestMatch index = %d (score %.3f), want 2", idx, score)
	}
	if idx, _ := BestMatch("anything", nil); idx != -1 {
		t.Fatalf("BestMatch on empty candidates = %d, want -1", idx)
	}
}

func TestLevenshtein(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"", "abc", 3},
		{"abc", "", 3},
		{"kitten", "sitting", 3},
		{"same", "same", 0},
	}
	for _, c := range cases {
		if got := Levenshtein(c.a, c.b); got != c.want {
			t.Errorf("Levenshtein(%q,%q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}
