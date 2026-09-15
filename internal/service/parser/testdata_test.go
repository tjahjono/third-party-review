package parser

import (
	"bytes"
	"testing"

	"github.com/google/uuid"
	"github.com/xuri/excelize/v2"

	"third-party-review/internal/helper"
	"third-party-review/internal/model"
)

// seededDomains mirrors what migration 000002 inserts, so parser tests run
// against the same names production will see.
func seededDomains() []*model.AssessmentDomain {
	out := make([]*model.AssessmentDomain, 0, len(helper.SeededDomainNames))
	for i, name := range helper.SeededDomainNames {
		out = append(out, &model.AssessmentDomain{
			ID:        testDomainID(i + 1),
			Name:      name,
			Slug:      slugify(name),
			SortOrder: i + 1,
		})
	}
	return out
}

func slugify(s string) string {
	var b []rune
	prevDash := false
	for _, r := range s {
		switch {
		case r >= 'A' && r <= 'Z':
			b = append(b, r+32)
			prevDash = false
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b = append(b, r)
			prevDash = false
		default:
			if !prevDash && len(b) > 0 {
				b = append(b, '-')
				prevDash = true
			}
		}
	}
	for len(b) > 0 && b[len(b)-1] == '-' {
		b = b[:len(b)-1]
	}
	return string(b)
}

// templateHeaders is the known 7-column template header row.
var templateHeaders = []string{
	"Question", "Assessor Remark", "Third Party Answer", "Third Party Remark",
	"Assessor Feedback", "Third Party Feedback", "Link Evidence",
}

// buildWorkbook writes rows into a real .xlsx in memory. mergeRows lists
// zero-based row indices that should be merged across all 7 columns, which is
// how domain dividers usually appear in a genuine TPSA file.
func buildWorkbook(t *testing.T, sheet string, rows [][]string, mergeRows ...int) []byte {
	t.Helper()
	f := excelize.NewFile()
	defer f.Close()

	idx, err := f.NewSheet(sheet)
	if err != nil {
		t.Fatalf("NewSheet: %v", err)
	}
	f.SetActiveSheet(idx)
	if sheet != "Sheet1" {
		f.DeleteSheet("Sheet1")
	}

	for r, row := range rows {
		for c, val := range row {
			cell, err := excelize.CoordinatesToCellName(c+1, r+1)
			if err != nil {
				t.Fatalf("CoordinatesToCellName: %v", err)
			}
			if err := f.SetCellStr(sheet, cell, val); err != nil {
				t.Fatalf("SetCellStr: %v", err)
			}
		}
	}
	for _, r := range mergeRows {
		start, _ := excelize.CoordinatesToCellName(1, r+1)
		end, _ := excelize.CoordinatesToCellName(7, r+1)
		if err := f.MergeCell(sheet, start, end); err != nil {
			t.Fatalf("MergeCell: %v", err)
		}
	}

	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		t.Fatalf("Write workbook: %v", err)
	}
	return buf.Bytes()
}

// standardRows is a realistic two-domain questionnaire: a title block, the
// header, then divider/question blocks.
func standardRows() [][]string {
	blank := make([]string, 7)
	divider := func(text string) []string {
		r := make([]string, 7)
		r[0] = text
		return r
	}
	q := func(question, remark, answer, vremark, afb, tpf, link string) []string {
		return []string{question, remark, answer, vremark, afb, tpf, link}
	}
	return [][]string{
		{"Third Party Security Assessment", "", "", "", "", "", ""},
		{"Vendor: Acme Corp", "", "", "", "", "", ""},
		blank,
		templateHeaders,
		divider("1. Network Security"),
		q("Do you segment your production network?", "Look for VLAN or SDN detail.",
			"Yes, production is on isolated VLANs reviewed quarterly.", "Diagram available on request.",
			"", "", "https://evidence.example/net-1"),
		q("Are firewall rules reviewed periodically?", "",
			"Industry standard firewalls are in place.", "", "", "", ""),
		blank,
		divider("2. Cloud Security"),
		q("Which cloud providers host customer data?", "Expect named regions.",
			"AWS eu-west-1 and eu-central-1.", "", "", "", "https://evidence.example/cloud-1"),
		q("How is cloud posture monitored?", "",
			"", "", "", "", ""),
		divider("8. AI Security"),
		q("Is customer data used to train models?", "Absence of a policy is itself a finding.",
			"No. We use a third-party model provider with training disabled.", "", "", "", ""),
	}
}

// testDomainID gives each seeded fixture domain a stable uuid, so the fixtures
// read as "domain 3" while the parser sees a real id.
func testDomainID(n int) uuid.UUID {
	var u uuid.UUID
	u[0] = 0xd0
	u[15] = byte(n)
	return u
}
