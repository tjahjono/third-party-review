package service

import (
	"fmt"
	"strings"

	"github.com/google/uuid"

	"third-party-review/internal/dto"
	"third-party-review/internal/helper"
	"third-party-review/internal/model"
)

// Validation rules for everything the service layer writes.
//
// These were methods on the model types. They are functions here because the
// model package is data only, and because validation is a service-layer
// decision: the same row is perfectly legal to read back after the rules
// change. Each one normalises in place (trimming whitespace, defaulting a
// status) and then reports every problem at once, so a form can highlight all
// of them in a single round trip.

// ValidateVendor checks the vendor is safe to persist.
func ValidateVendor(v *model.Vendor) error {
	var errs helper.ValidationErrors
	v.Name = strings.TrimSpace(v.Name)
	v.ContactName = strings.TrimSpace(v.ContactName)
	v.ContactEmail = strings.TrimSpace(v.ContactEmail)
	if v.Name == "" {
		errs.Add("name", "Vendor name is required.")
	} else if len(v.Name) > 200 {
		errs.Add("name", "Vendor name must be 200 characters or fewer.")
	}
	if v.ContactEmail != "" && !strings.Contains(v.ContactEmail, "@") {
		errs.Add("contact_email", "Contact email does not look like an email address.")
	}
	return errs.OrNil()
}

// ValidateAssessment checks the assessment is safe to persist.
func ValidateAssessment(a *model.Assessment) error {
	var errs helper.ValidationErrors
	a.Title = strings.TrimSpace(a.Title)
	if a.VendorID == uuid.Nil {
		errs.Add("vendor_id", "Select a vendor for this assessment.")
	}
	if a.Title == "" {
		errs.Add("title", "Assessment title is required.")
	} else if len(a.Title) > 200 {
		errs.Add("title", "Assessment title must be 200 characters or fewer.")
	}
	if a.Status == "" {
		a.Status = model.StatusUploaded
	}
	if !helper.ValidAssessmentStatus(a.Status) {
		errs.Add("status", "Unknown assessment status "+string(a.Status)+".")
	}
	return errs.OrNil()
}

// ValidateQuestion checks the question is safe to persist.
func ValidateQuestion(q *model.Question) error {
	var errs helper.ValidationErrors
	q.QuestionText = strings.TrimSpace(q.QuestionText)
	if q.AssessmentID == uuid.Nil {
		errs.Add("assessment_id", "Question is not attached to an assessment.")
	}
	if q.DomainID == uuid.Nil {
		errs.Add("domain_id", "Every question must belong to exactly one domain.")
	}
	if q.QuestionText == "" {
		errs.Add("question_text", "Question text cannot be empty.")
	}
	if q.ReviewStatus == "" {
		q.ReviewStatus = model.ReviewPending
	}
	if !helper.ValidReviewStatus(q.ReviewStatus) {
		errs.Add("review_status", "Unknown review status "+string(q.ReviewStatus)+".")
	}
	return errs.OrNil()
}

// ValidateRubric checks the rubric is safe to persist.
func ValidateRubric(r *model.Rubric) error {
	var errs helper.ValidationErrors
	r.Name = strings.TrimSpace(r.Name)
	r.Content = strings.TrimSpace(r.Content)
	if r.Name == "" {
		errs.Add("name", "Give the rubric a name so you can pick it later.")
	}
	if r.Content == "" {
		errs.Add("content", "Rubric content cannot be empty.")
	}
	return errs.OrNil()
}

// ValidateColumnMapping ensures the mapping is usable: the Question column
// must be present and no two fields may claim the same column.
func ValidateColumnMapping(m *model.ColumnMapping) error {
	var errs helper.ValidationErrors
	if m == nil || len(m.Bindings) == 0 {
		errs.Add("mapping", "No columns were mapped. Map at least the Question column.")
		return errs.OrNil()
	}
	if _, ok := m.Bindings[model.FieldQuestion]; !ok {
		errs.Add(string(model.FieldQuestion), "Couldn't detect a Question column - please map it manually.")
	}
	seen := map[int]model.QuestionField{}
	for field, b := range m.Bindings {
		if prev, dup := seen[b.Index]; dup {
			errs.Add(string(field), fmt.Sprintf("Column %d is already mapped to %s. Each column may be used once.", b.Index+1, prev))
			continue
		}
		seen[b.Index] = field
		if _, known := dto.TemplateFieldByName(field); !known {
			errs.Add(string(field), "Unknown field "+string(field)+".")
		}
	}
	return errs.OrNil()
}
