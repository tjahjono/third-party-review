package parser

import (
	"sort"
	"strings"

	"third-party-review/internal/dto"
	"third-party-review/internal/helper"
	"third-party-review/internal/model"
	"third-party-review/pkg/fuzzy"
)

// autoMapThreshold is the score a header must reach before it is pre-selected
// for the user. Below it the column is left unmapped rather than guessed
// wrongly, because an unmapped dropdown is a smaller correction than an
// incorrect one the user has to notice first.
const autoMapThreshold = 0.70

// headerScanRows limits how far down the sheet the header search looks. Real
// templates carry a title block above the table, but never more than a screen.
const headerScanRows = 25

// DetectHeaderRow finds the row most likely to be the table header. It scores
// each candidate row by how many of its cells look like known template
// columns, preferring earlier rows when scores tie.
func DetectHeaderRow(g *dto.Grid) (int, float64) {
	limit := min(len(g.Rows), headerScanRows)
	bestRow, bestScore := -1, 0.0

	for i := 0; i < limit; i++ {
		score := scoreHeaderRow(g.Rows[i])
		if score > bestScore {
			bestRow, bestScore = i, score
		}
	}
	if bestRow < 0 {
		return 0, 0
	}
	return bestRow, bestScore
}

// scoreHeaderRow sums the best template match for each non-empty cell and
// normalises by the number of template fields, so a row matching many known
// columns outranks one matching a single column well.
func scoreHeaderRow(row []string) float64 {
	total, filled := 0.0, 0
	for _, cell := range row {
		norm := helper.NormalizeHeader(cell)
		if norm == "" {
			continue
		}
		filled++
		// Long cells are prose, not headers.
		if len(norm) > 80 {
			continue
		}
		if _, score := matchField(norm); score >= autoMapThreshold {
			total += score
		}
	}
	if filled == 0 {
		return 0
	}
	return total / float64(len(dto.TemplateFields))
}

// matchField returns the template field that best matches a normalised header
// and its score.
func matchField(normHeader string) (model.QuestionField, float64) {
	var (
		best      model.QuestionField
		bestScore float64
	)
	for _, tf := range dto.TemplateFields {
		for _, alias := range tf.Aliases {
			score := fuzzy.Ratio(normHeader, alias)
			if cs := fuzzy.Contains(normHeader, alias); cs > score {
				score = cs
			}
			if score > bestScore {
				best, bestScore = tf.Field, score
			}
		}
	}
	return best, bestScore
}

// AutoMap produces the best-guess column mapping for a header row, plus the
// per-column candidate list the UI renders as pre-filled dropdowns.
//
// Assignment is greedy by descending score with each field and each column
// used at most once. That matters for files carrying both "Assessor Remark"
// and "Assessor Feedback": scoring each column independently would let both
// claim the same field, whereas resolving globally gives each its own.
func AutoMap(g *dto.Grid, headerRow int) (*model.ColumnMapping, []dto.HeaderCandidate) {
	headers := make([]string, 0, g.Width())
	if headerRow >= 0 && headerRow < len(g.Rows) {
		headers = append(headers, g.Rows[headerRow]...)
	}

	type pair struct {
		col   int
		field model.QuestionField
		score float64
	}
	var pairs []pair
	for col, h := range headers {
		norm := helper.NormalizeHeader(h)
		if norm == "" || len(norm) > 80 {
			continue
		}
		for _, tf := range dto.TemplateFields {
			for _, alias := range tf.Aliases {
				score := fuzzy.Ratio(norm, alias)
				if cs := fuzzy.Contains(norm, alias); cs > score {
					score = cs
				}
				if score >= autoMapThreshold {
					pairs = append(pairs, pair{col: col, field: tf.Field, score: score})
				}
			}
		}
	}
	// Highest score first; ties broken left-to-right so the template's own
	// column order wins when two columns match a field equally well.
	sort.SliceStable(pairs, func(i, j int) bool {
		if pairs[i].score != pairs[j].score {
			return pairs[i].score > pairs[j].score
		}
		return pairs[i].col < pairs[j].col
	})

	mapping := &model.ColumnMapping{
		SheetName: g.SheetName,
		HeaderRow: headerRow,
		Bindings:  map[model.QuestionField]model.ColumnBinding{},
	}
	usedCol := map[int]bool{}
	best := map[int]pair{} // best accepted pair per column, for the UI

	for _, p := range pairs {
		if usedCol[p.col] {
			continue
		}
		if _, taken := mapping.Bindings[p.field]; taken {
			continue
		}
		mapping.Bindings[p.field] = model.ColumnBinding{
			Field:      p.field,
			Index:      p.col,
			Header:     strings.TrimSpace(headers[p.col]),
			Confidence: p.score,
		}
		usedCol[p.col] = true
		best[p.col] = p
	}

	candidates := make([]dto.HeaderCandidate, 0, len(headers))
	for col, h := range headers {
		c := dto.HeaderCandidate{Index: col, Header: strings.TrimSpace(h)}
		if p, ok := best[col]; ok {
			c.Suggested, c.Confidence = p.field, p.score
		}
		candidates = append(candidates, c)
		if c.Suggested == "" && strings.TrimSpace(h) != "" {
			mapping.IgnoredColumns = append(mapping.IgnoredColumns, strings.TrimSpace(h))
		}
	}
	return mapping, candidates
}

// FallbackQuestionColumn picks the column most likely to hold questions when
// header matching found none. It scores columns by how much prose they carry
// below the header, since a question column is long text repeated on most rows.
// Returning a guess the user can correct beats refusing the upload outright.
func FallbackQuestionColumn(g *dto.Grid, headerRow int) (int, bool) {
	if g.Width() == 0 {
		return 0, false
	}
	bestCol, bestScore := -1, 0.0
	for col := 0; col < g.Width(); col++ {
		var filled, totalLen, questionMarks int
		for row := headerRow + 1; row < len(g.Rows); row++ {
			cell := strings.TrimSpace(g.Cell(row, col))
			if cell == "" {
				continue
			}
			filled++
			totalLen += len(cell)
			if strings.Contains(cell, "?") {
				questionMarks++
			}
		}
		if filled < 2 {
			continue
		}
		avgLen := float64(totalLen) / float64(filled)
		if avgLen < 15 {
			continue // too short to be a question
		}
		// Question marks are a strong positive signal; leftmost columns are a
		// weak one, since the template puts Question first.
		score := avgLen + 40*float64(questionMarks)/float64(filled) - float64(col)
		if score > bestScore {
			bestCol, bestScore = col, score
		}
	}
	if bestCol < 0 {
		return 0, false
	}
	return bestCol, true
}
