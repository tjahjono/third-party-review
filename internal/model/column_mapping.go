package model

// ColumnBinding records that one spreadsheet column supplies one logical
// field. Stored inside the assessments.column_mapping JSONB column.
type ColumnBinding struct {
	Field QuestionField `json:"field"`
	// Index is the zero-based column index in the source sheet.
	Index int `json:"index"`
	// Header is the raw header text as it appeared in the file.
	Header string `json:"header"`
	// Confidence is the auto-match score in [0,1]. 1 means an exact alias hit.
	// Zero after a manual override by the user.
	Confidence float64 `json:"confidence"`
	// Manual is true when the user chose this binding rather than the matcher.
	Manual bool `json:"manual"`
}

// ColumnMapping is the confirmed translation from sheet columns to question
// fields, persisted on the Assessment row for auditability.
type ColumnMapping struct {
	// SheetName is the worksheet the questions were read from (empty for CSV).
	SheetName string `json:"sheet_name,omitempty"`
	// HeaderRow is the zero-based row index the header was found on.
	HeaderRow int `json:"header_row"`
	// Bindings is keyed by logical field. Unmapped fields are absent.
	Bindings map[QuestionField]ColumnBinding `json:"bindings"`
	// IgnoredColumns lists headers present in the file but not mapped.
	IgnoredColumns []string `json:"ignored_columns,omitempty"`
	// ConfirmedAt is set when a human accepted the mapping.
	ConfirmedAt string `json:"confirmed_at,omitempty"`
}

// No methods: the accessor that used to live here is helper.MappedColumn, and
// the MarshalJSON that used to live here only restated the default encoding -
// Bindings is keyed by a string type, which encoding/json handles natively.
