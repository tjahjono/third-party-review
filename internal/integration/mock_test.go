package integration

import (
	"third-party-review/internal/domain"
	"third-party-review/internal/service/aiclient"
)

// newMock returns the deterministic reviewer shipped with the app. Using the
// production mock rather than a test-local double means these tests exercise
// exactly the code path a developer gets with AI_PROVIDER=mock.
func newMock() domain.AIReviewer { return aiclient.NewMock() }

// newFailingMock returns a mock whose batch call fails for any batch
// containing the given text, so the per-question fallback can be tested.
func newFailingMock(failOn string) domain.AIReviewer {
	m := aiclient.NewMock()
	m.FailOn = failOn
	return m
}

// newDroppingMock returns a mock that silently omits the given question ids
// from batch responses, simulating a model that loses items.
func newDroppingMock(ids ...int64) domain.AIReviewer {
	m := aiclient.NewMock()
	m.SkipQuestionIDs = map[int64]bool{}
	for _, id := range ids {
		m.SkipQuestionIDs[id] = true
	}
	return m
}
