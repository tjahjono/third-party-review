package handler

import "github.com/google/uuid"

// uuidStr renders an id for a URL path. It exists so the handlers have one
// spelling for this rather than each reaching for .String() inline, and so a
// zero id becomes an empty segment rather than the all-zeroes uuid, which
// would look like a real record.
func uuidStr(id uuid.UUID) string {
	if id == uuid.Nil {
		return ""
	}
	return id.String()
}
