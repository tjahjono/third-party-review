package dto

// BulkFinalizeResult reports what a bulk sign-off actually did.
type BulkFinalizeResult struct {
	Finalized int `json:"finalized"`
	// Skipped counts questions passed over because they had no draft to sign
	// off. Accepting a blank draft would put an empty finding into the record,
	// which reads as reviewed.
	Skipped int `json:"skipped"`
	// AlreadyFinal counts questions a human had already signed.
	AlreadyFinal int `json:"already_final"`
}

// SignOffProgress is the reviewer-facing state of an assessment's sign-off.
type SignOffProgress struct {
	Total     int `json:"total"`
	Finalized int `json:"finalized"`
	Pending   int `json:"pending"`
	// NoDraft counts pending questions the AI left without a draft, which have
	// to be written by hand before the assessment can be closed.
	NoDraft      int  `json:"no_draft"`
	Percent      int  `json:"percent"`
	ReadyToClose bool `json:"ready_to_close"`
}
