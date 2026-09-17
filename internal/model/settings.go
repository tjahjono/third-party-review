package model

import (
	"time"

	"github.com/google/uuid"
)

// RiskMatrix is the score-to-band mapping shown throughout the app: which raw
// 1-5 AI risk scores count as Low, Medium, High or Critical. It is a single,
// app-wide setting - this is a single-tenant tool (see project non-goals), so
// there is exactly one matrix for everyone - editable on the Settings page
// and applied immediately everywhere a band is computed or displayed:
// question cards, domain and overall summary scores, the dashboard, and the
// CSV/Excel exports. Table: app_settings (a single row).
type RiskMatrix struct {
	// MediumMin is the lowest score that counts as Medium; every score below
	// it is Low. HighMin and CriticalMin work the same way for their bands.
	// They must be non-decreasing (MediumMin <= HighMin <= CriticalMin);
	// setting two equal collapses that band out entirely, e.g. HighMin ==
	// CriticalMin means a score never lands on High, jumping straight from
	// Medium to Critical.
	MediumMin   RiskScore `json:"medium_min"`
	HighMin     RiskScore `json:"high_min"`
	CriticalMin RiskScore `json:"critical_min"`

	UpdatedAt time.Time  `json:"updated_at"`
	UpdatedBy *uuid.UUID `json:"updated_by,omitempty"`
}

// DefaultRiskMatrix is the matrix TPSA Reviewer ships with, matching the
// bands the app originally hardcoded: 1-2 Low, 3 Medium, 4 High, 5 Critical.
// It seeds the app_settings row on first migration and is what a fresh
// process assumes before it has loaded the persisted setting.
var DefaultRiskMatrix = RiskMatrix{MediumMin: 3, HighMin: 4, CriticalMin: 5}
