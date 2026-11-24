package htmlpage

// Status, icon, and CSS class values shared by page view models.
const (
	statusInProgress = "in_progress"
	statusTimedOut   = "timed_out"
	statusFailed     = "failed"
	statusPassed     = "passed"
	statusFlaky      = "flaky"
	statusSkipped    = "skipped"
	statusPending    = "pending"
	statusRunning    = "running"
	statusAbandoned  = "abandoned"

	iconLive   = "live"
	iconPass   = "pass"
	iconFail   = "fail"
	iconLeased = "leased"
	iconRetest = "retest"

	modeEmpty = "empty"

	clsUnitFail    = "unit-fail"
	clsUnitPass    = "unit-pass"
	clsUnitSkipped = "unit-skipped"
	clsUnitPending = "unit-pending"
)
