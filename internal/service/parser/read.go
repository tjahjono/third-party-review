package parser

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/xuri/excelize/v2"

	"third-party-review/internal/dto"
	"third-party-review/internal/helper"
)

// maxRows caps how much of a file is read. A TPSA questionnaire is hundreds of
// rows; anything far beyond that is a sign the wrong file was uploaded.
const maxRows = 20000

// ReadGrid reads an uploaded file into a Grid, choosing the reader from the
// filename extension and falling back to content sniffing when the extension
// is missing or wrong.
func ReadGrid(r io.Reader, filename string) (*dto.Grid, error) {
	data, err := io.ReadAll(io.LimitReader(r, 200<<20))
	if err != nil {
		return nil, helper.NewParseError("Couldn't read the uploaded file.", "Try uploading it again.", err)
	}
	if len(data) == 0 {
		return nil, helper.NewParseError("The uploaded file is empty.", "Check you selected the right file.", nil)
	}

	switch strings.ToLower(filepath.Ext(filename)) {
	case ".xlsx", ".xlsm", ".xltx":
		return readExcel(data)
	case ".csv", ".tsv", ".txt":
		return readCSV(data, filepath.Ext(filename))
	}

	// Unknown extension: sniff. XLSX files are ZIP archives, so they start PK.
	if bytes.HasPrefix(data, []byte("PK\x03\x04")) {
		return readExcel(data)
	}
	if utf8.Valid(data) {
		return readCSV(data, "")
	}
	return nil, helper.NewParseError(
		"Couldn't tell what kind of file this is.",
		"Upload the questionnaire as .xlsx or .csv.", nil)
}

// readExcel reads the first worksheet that contains data. Merged cells are
// expanded so every covered cell carries the merged value: in a real TPSA
// workbook a domain divider is usually a merged row, and a multi-row question
// often merges its answer cell.
func readExcel(data []byte) (*dto.Grid, error) {
	f, err := excelize.OpenReader(bytes.NewReader(data))
	if err != nil {
		return nil, helper.NewParseError(
			"Couldn't open that workbook.",
			"Make sure it is a real .xlsx file and not renamed from another format.", err)
	}
	defer f.Close()

	names := f.GetSheetList()
	if len(names) == 0 {
		return nil, helper.NewParseError("The workbook has no worksheets.", "", nil)
	}

	// Pick the sheet with the most populated rows rather than always the
	// first: real workbooks often lead with a cover or instructions sheet.
	var (
		bestName string
		bestRows [][]string
		bestFill int
	)
	for _, name := range names {
		rows, err := f.GetRows(name)
		if err != nil {
			continue
		}
		if fill := countFilled(rows); fill > bestFill {
			bestName, bestRows, bestFill = name, rows, fill
		}
	}
	if bestFill == 0 {
		return nil, helper.NewParseError("Every worksheet in that workbook is empty.", "", nil)
	}

	grid := &dto.Grid{SheetName: bestName, SheetNames: names, Rows: normalizeRows(bestRows)}
	if err := expandMerged(f, bestName, grid); err != nil {
		return nil, err
	}
	return grid, nil
}

// ReadExcelSheet re-reads a specific worksheet, used when the user picks a
// different sheet than the one auto-selected.
func ReadExcelSheet(data []byte, sheet string) (*dto.Grid, error) {
	f, err := excelize.OpenReader(bytes.NewReader(data))
	if err != nil {
		return nil, helper.NewParseError("Couldn't open that workbook.", "", err)
	}
	defer f.Close()

	rows, err := f.GetRows(sheet)
	if err != nil {
		return nil, helper.NewParseError(
			fmt.Sprintf("Couldn't read worksheet %q.", sheet),
			"Pick a different sheet.", err)
	}
	grid := &dto.Grid{SheetName: sheet, SheetNames: f.GetSheetList(), Rows: normalizeRows(rows)}
	if err := expandMerged(f, sheet, grid); err != nil {
		return nil, err
	}
	return grid, nil
}

// expandMerged copies each merged region's value into every cell it covers.
// excelize reports the value only in the top-left cell, which would otherwise
// make a merged divider row look blank.
func expandMerged(f *excelize.File, sheet string, g *dto.Grid) error {
	merged, err := f.GetMergeCells(sheet)
	if err != nil {
		// A workbook without merge metadata is normal, not an error.
		return nil
	}
	for _, m := range merged {
		startCol, startRow, err := excelize.CellNameToCoordinates(m.GetStartAxis())
		if err != nil {
			continue
		}
		endCol, endRow, err := excelize.CellNameToCoordinates(m.GetEndAxis())
		if err != nil {
			continue
		}
		value := m.GetCellValue()
		if strings.TrimSpace(value) == "" {
			continue
		}
		for r := startRow - 1; r < endRow && r < len(g.Rows); r++ {
			for c := startCol - 1; c < endCol && c < len(g.Rows[r]); c++ {
				if strings.TrimSpace(g.Rows[r][c]) == "" {
					g.Rows[r][c] = value
				}
			}
		}
	}
	return nil
}

// readCSV reads delimited text, auto-detecting comma, semicolon or tab.
func readCSV(data []byte, ext string) (*dto.Grid, error) {
	// Strip a UTF-8 BOM, which Excel writes when exporting CSV on Windows and
	// which would otherwise corrupt the first header.
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})

	delim := detectDelimiter(data, ext)
	cr := csv.NewReader(bytes.NewReader(data))
	cr.Comma = delim
	// Real questionnaires have ragged rows and multi-line answers; both are
	// expected, not errors.
	cr.FieldsPerRecord = -1
	cr.LazyQuotes = true

	var rows [][]string
	for len(rows) < maxRows {
		rec, err := cr.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			var pe *csv.ParseError
			if errors.As(err, &pe) {
				return nil, helper.NewParseError(
					fmt.Sprintf("Couldn't parse line %d of the CSV.", pe.Line),
					"Check for an unclosed quote on that line, or re-export the file as .xlsx.", err)
			}
			return nil, helper.NewParseError("Couldn't parse the CSV.", "Try re-exporting it as .xlsx.", err)
		}
		rows = append(rows, rec)
	}
	if countFilled(rows) == 0 {
		return nil, helper.NewParseError("The file has no data rows.", "", nil)
	}
	return &dto.Grid{Rows: normalizeRows(rows)}, nil
}

// detectDelimiter counts candidate separators on the first few non-empty lines
// and picks the most consistently repeated one.
func detectDelimiter(data []byte, ext string) rune {
	if strings.EqualFold(ext, ".tsv") {
		return '\t'
	}
	sample := data
	if len(sample) > 64<<10 {
		sample = sample[:64<<10]
	}
	lines := strings.SplitN(string(sample), "\n", 10)

	best, bestCount := ',', 0
	for _, d := range []rune{',', ';', '\t', '|'} {
		total := 0
		for _, ln := range lines {
			total += strings.Count(ln, string(d))
		}
		if total > bestCount {
			best, bestCount = d, total
		}
	}
	return best
}

// normalizeRows trims trailing empty rows, right-pads every row to the widest
// one, and cleans cell whitespace so indexing and comparison are safe.
func normalizeRows(rows [][]string) [][]string {
	if len(rows) > maxRows {
		rows = rows[:maxRows]
	}
	width := 0
	last := -1
	for i, r := range rows {
		if len(r) > width {
			width = len(r)
		}
		for _, c := range r {
			if strings.TrimSpace(c) != "" {
				last = i
				break
			}
		}
	}
	if last < 0 {
		return nil
	}
	rows = rows[:last+1]

	out := make([][]string, len(rows))
	for i, r := range rows {
		row := make([]string, width)
		for j := range row {
			if j < len(r) {
				row[j] = cleanCell(r[j])
			}
		}
		out[i] = row
	}
	return out
}

// cleanCell normalises whitespace without destroying deliberate line breaks,
// which carry meaning in multi-paragraph vendor answers.
func cleanCell(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = strings.ReplaceAll(s, " ", " ") // non-breaking space
	lines := strings.Split(s, "\n")
	for i, ln := range lines {
		lines[i] = strings.TrimRight(strings.TrimLeft(ln, " \t"), " \t")
	}
	return strings.Trim(strings.Join(lines, "\n"), "\n \t")
}

// countFilled reports how many rows contain at least one non-empty cell.
func countFilled(rows [][]string) int {
	n := 0
	for _, r := range rows {
		for _, c := range r {
			if strings.TrimSpace(c) != "" {
				n++
				break
			}
		}
	}
	return n
}
