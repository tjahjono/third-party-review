package parser

import (
	"fmt"
	"regexp"
	"strings"

	"third-party-review/internal/domain"
	"third-party-review/pkg/fuzzy"
)

// dividerThreshold is how closely a cell must match a seeded domain name to be
// treated as a section divider. It is higher than the column-mapping threshold
// because a false divider silently reassigns every question below it, which is
// a much more damaging error than an unmapped column.
const dividerThreshold = 0.78

// sectionNumberPrefix strips the leading numbering real templates use, e.g.
// "1. Network Security", "2) Application Security", "Section 3 - Data Security".
var sectionNumberPrefix = regexp.MustCompile(`^\s*(?:section\s*)?\d+\s*[.)\-:]?\s*`)

// DetectSections classifies every row below the header as a divider, a
// question or blank, and groups the question rows into domain sections.
//
// A divider is recognised structurally as well as textually. In a real
// workbook the heading is usually a row merged across every column, so after
// merge expansion the domain name appears in the Question column too - the
// heading cannot be identified by "the question cell is empty" alone. The rule
// used here is: some cell reads as a domain heading, and no other cell in the
// row carries content that differs from it. A question row always fails the
// second half, so a question whose text merely mentions "Cloud Security" never
// opens a section.
func DetectSections(g *Grid, mapping *domain.ColumnMapping, domains []*domain.AssessmentDomain) ([]Row, []Section, []string) {
	var (
		rows     []Row
		sections []Section
		warnings []string
	)
	qCol, hasQ := mapping.Index(domain.FieldQuestion)

	var (
		current    *Section
		unassigned []int
	)

	flush := func() {
		if current != nil && current.RowCount > 0 {
			sections = append(sections, *current)
		}
		current = nil
	}

	for i := mapping.HeaderRow + 1; i < len(g.Rows); i++ {
		row := Row{Index: i, Cells: g.Rows[i]}

		if isBlankRow(g.Rows[i]) {
			row.Kind = RowBlank
			rows = append(rows, row)
			continue
		}

		if d, text, score, col := matchDivider(g.Rows[i], domains); d != nil && !hasDistinctContent(g.Rows[i], col) {
			flush()
			current = &Section{
				DomainID:   d.ID,
				DomainName: d.Name,
				DividerRow: i,
				FirstRow:   i + 1,
				LastRow:    i + 1,
			}
			row.Kind = RowDivider
			row.DomainID = d.ID
			row.DomainName = d.Name
			row.DividerText = text
			row.DividerScore = score
			rows = append(rows, row)
			if score < 0.92 {
				warnings = append(warnings, fmt.Sprintf(
					"Row %d matched %q loosely (%.0f%% confidence). Check the boundary is right.",
					i+1, d.Name, score*100))
			}
			continue
		}

		questionCell := ""
		if hasQ {
			questionCell = strings.TrimSpace(g.Cell(i, qCol))
		}
		if questionCell == "" {
			// Content but no question text: a stray note, or the tail of a
			// merged answer. Skipping is safer than inventing a question.
			row.Kind = RowBlank
			rows = append(rows, row)
			continue
		}

		row.Kind = RowQuestion
		row.Values = extractValues(g, i, mapping)
		if current != nil {
			row.DomainID = current.DomainID
			row.DomainName = current.DomainName
			current.LastRow = i
			current.RowCount++
		} else {
			unassigned = append(unassigned, len(rows))
		}
		rows = append(rows, row)
	}
	flush()

	// Questions appearing before the first divider still need a domain.
	// Attribute them to the first detected section (or the first seeded domain
	// when no divider was found at all) and mark the section inferred so the
	// preview highlights it for checking.
	if len(unassigned) > 0 {
		fallbackID, fallbackName := int64(0), ""
		if len(sections) > 0 {
			fallbackID, fallbackName = sections[0].DomainID, sections[0].DomainName
		} else if len(domains) > 0 {
			fallbackID, fallbackName = domains[0].ID, domains[0].Name
		}
		if fallbackID != 0 {
			first, last := rows[unassigned[0]].Index, rows[unassigned[len(unassigned)-1]].Index
			for _, ri := range unassigned {
				rows[ri].DomainID = fallbackID
				rows[ri].DomainName = fallbackName
			}
			sections = append([]Section{{
				DomainID:   fallbackID,
				DomainName: fallbackName,
				DividerRow: -1,
				FirstRow:   first,
				LastRow:    last,
				RowCount:   len(unassigned),
				Inferred:   true,
			}}, sections...)
			warnings = append(warnings, fmt.Sprintf(
				"%d question(s) appear before the first domain heading and were put in %q. Reassign them if that's wrong.",
				len(unassigned), fallbackName))
		}
	}

	if len(sections) == 0 {
		warnings = append(warnings, "No domain section headings were detected. Assign each question a domain before continuing.")
	} else if len(sections) < len(domains)/2 {
		warnings = append(warnings, fmt.Sprintf(
			"Only %d of %d domains were detected as section headings. Check the file uses the standard layout.",
			len(sections), len(domains)))
	}

	return rows, sections, warnings
}

// hasDistinctContent reports whether any cell other than skip carries text that
// differs from the cell at skip. A heading merged across the row expands to the
// same value in every cell, so it reports false; a question row with an answer
// reports true.
func hasDistinctContent(cells []string, skip int) bool {
	var ref string
	if skip >= 0 && skip < len(cells) {
		ref = strings.TrimSpace(cells[skip])
	}
	for i, c := range cells {
		if i == skip {
			continue
		}
		v := strings.TrimSpace(c)
		if v != "" && v != ref {
			return true
		}
	}
	return false
}

// matchDivider looks for a cell that reads as a domain heading. It returns the
// matched domain, the raw cell text, the score and the cell's column index.
//
// A substring hit only counts when the cell is not much longer than the domain
// name itself. Without that guard a sentence such as "Describe how Cloud
// Security responsibilities are split with your provider" scores high enough
// on containment alone to be mistaken for a heading.
func matchDivider(cells []string, domains []*domain.AssessmentDomain) (*domain.AssessmentDomain, string, float64, int) {
	var (
		best      *domain.AssessmentDomain
		bestText  string
		bestScore float64
		bestCol   = -1
	)
	for col, cell := range cells {
		raw := strings.TrimSpace(cell)
		if raw == "" {
			continue
		}
		// A divider is a heading, not a paragraph.
		if len([]rune(raw)) > 80 {
			continue
		}
		norm := domain.NormalizeHeader(sectionNumberPrefix.ReplaceAllString(strings.ToLower(raw), ""))
		if norm == "" {
			continue
		}
		for _, d := range domains {
			for _, candidate := range domainAliases(d) {
				score := fuzzy.Ratio(norm, candidate)
				// Containment is only evidence of a heading when the cell is
				// roughly the length of the name it contains.
				if len(norm) <= int(1.6*float64(len(candidate)))+4 {
					if cs := fuzzy.Contains(norm, candidate); cs > score {
						score = cs
					}
				}
				if score > bestScore {
					best, bestText, bestScore, bestCol = d, raw, score, col
				}
			}
		}
	}
	if bestScore < dividerThreshold {
		return nil, "", 0, -1
	}
	return best, bestText, bestScore, bestCol
}

// domainAliases returns the strings a divider cell may be compared against for
// one domain: its name, its slug spelled out, and a comma-free variant. The
// last one matters for "Change, Performance and Capacity Management", which
// files abbreviate and repunctuate freely.
func domainAliases(d *domain.AssessmentDomain) []string {
	name := domain.NormalizeHeader(d.Name)
	aliases := []string{name}
	if slug := strings.ReplaceAll(d.Slug, "-", " "); slug != name {
		aliases = append(aliases, slug)
	}
	// Drop conjunctions so "Change, Performance and Capacity Management"
	// still matches "Change / Performance / Capacity Management".
	if stripped := strings.TrimSpace(strings.ReplaceAll(name, " and ", " ")); stripped != name {
		aliases = append(aliases, stripped)
	}
	return aliases
}

// extractValues pulls the mapped field values out of one source row.
func extractValues(g *Grid, rowIdx int, mapping *domain.ColumnMapping) map[domain.QuestionField]string {
	values := make(map[domain.QuestionField]string, len(mapping.Bindings))
	for field, b := range mapping.Bindings {
		values[field] = strings.TrimSpace(g.Cell(rowIdx, b.Index))
	}
	return values
}

// isBlankRow reports whether every cell in the row is empty.
func isBlankRow(row []string) bool {
	for _, c := range row {
		if strings.TrimSpace(c) != "" {
			return false
		}
	}
	return true
}
