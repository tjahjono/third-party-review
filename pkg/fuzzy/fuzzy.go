// Package fuzzy provides small, dependency-free string similarity helpers used
// to match spreadsheet headers and section titles against known names. It has
// no domain knowledge, so it lives under /pkg.
package fuzzy

import "strings"

// Ratio returns a similarity score in [0,1] combining normalised Levenshtein
// distance with a token-overlap measure. The blend matters for spreadsheet
// headers: "3rd Party Answer" and "Third Party Answer" share few characters
// but most tokens, while "Assessor Remark" and "Assessor Remarks" share almost
// every character. Either signal alone misses one of those cases.
func Ratio(a, b string) float64 {
	a, b = strings.TrimSpace(strings.ToLower(a)), strings.TrimSpace(strings.ToLower(b))
	if a == "" || b == "" {
		return 0
	}
	if a == b {
		return 1
	}
	lev := levenshteinRatio(a, b)
	tok := tokenRatio(a, b)
	// Take the better of the two views rather than a fixed blend. A pure
	// character comparison handles "3rd Party Answer" vs "Third Party Answer";
	// a token comparison handles reordering such as "Link Evidence" vs
	// "Evidence Link", where character distance is misleadingly large. Blending
	// them at a fixed ratio drags both good cases below any usable threshold.
	blended := 0.2*lev + 0.8*tok
	if blended > lev {
		return blended
	}
	return lev
}

// Contains reports a boosted score when one string wholly contains the other,
// which is common for headers like "Question (control statement)".
func Contains(haystack, needle string) float64 {
	h, n := strings.ToLower(haystack), strings.ToLower(needle)
	if n == "" || h == "" {
		return 0
	}
	if !strings.Contains(h, n) {
		return 0
	}
	// Longer needles relative to the haystack are stronger evidence.
	return 0.75 + 0.25*float64(len(n))/float64(len(h))
}

// BestMatch returns the index and score of the candidate most similar to s.
// It returns -1 when candidates is empty.
func BestMatch(s string, candidates []string) (int, float64) {
	best, bestScore := -1, 0.0
	for i, c := range candidates {
		score := Ratio(s, c)
		if cs := Contains(s, c); cs > score {
			score = cs
		}
		if score > bestScore {
			best, bestScore = i, score
		}
	}
	return best, bestScore
}

// levenshteinRatio is 1 - distance/maxLen, floored at zero.
func levenshteinRatio(a, b string) float64 {
	d := Levenshtein(a, b)
	maxLen := len([]rune(a))
	if l := len([]rune(b)); l > maxLen {
		maxLen = l
	}
	if maxLen == 0 {
		return 1
	}
	r := 1 - float64(d)/float64(maxLen)
	if r < 0 {
		return 0
	}
	return r
}

// Levenshtein returns the edit distance between a and b using a two-row
// rolling buffer.
func Levenshtein(a, b string) int {
	ar, br := []rune(a), []rune(b)
	if len(ar) == 0 {
		return len(br)
	}
	if len(br) == 0 {
		return len(ar)
	}
	prev := make([]int, len(br)+1)
	curr := make([]int, len(br)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ar); i++ {
		curr[0] = i
		for j := 1; j <= len(br); j++ {
			cost := 1
			if ar[i-1] == br[j-1] {
				cost = 0
			}
			curr[j] = min3(curr[j-1]+1, prev[j]+1, prev[j-1]+cost)
		}
		prev, curr = curr, prev
	}
	return prev[len(br)]
}

// tokenRatio is the Sorensen-Dice coefficient over whitespace-separated
// tokens, with a small allowance for near-identical tokens (plurals, typos).
func tokenRatio(a, b string) float64 {
	at, bt := strings.Fields(a), strings.Fields(b)
	if len(at) == 0 || len(bt) == 0 {
		return 0
	}
	used := make([]bool, len(bt))
	matches := 0.0
	for _, t := range at {
		bestIdx, bestScore := -1, 0.0
		for j, u := range bt {
			if used[j] {
				continue
			}
			s := levenshteinRatio(t, u)
			if s > bestScore {
				bestIdx, bestScore = j, s
			}
		}
		// 0.8 tolerates "remark"/"remarks" but rejects unrelated words.
		if bestIdx >= 0 && bestScore >= 0.8 {
			used[bestIdx] = true
			matches += bestScore
		}
	}
	return 2 * matches / float64(len(at)+len(bt))
}

func min3(a, b, c int) int {
	if b < a {
		a = b
	}
	if c < a {
		a = c
	}
	return a
}
