package identity

const (
	retryFailed = "failed"
	retryFlaky  = "flaky"
)

// collapseAttemptRetries preserves one terminal outcome per identity/lease attempt.
// The dispatch action emits raw Playwright results, including built-in retries.
func collapseAttemptRetries(g group, cases []observation) []observation {
	out := []observation{}
	indices := map[string]int{}
	failed := map[string]bool{}
	for _, o := range cases {
		rawTitle := o.FullTitle
		if rawTitle == "" {
			rawTitle = o.Title
		}
		key := string(StableKey(g.Repository, g.Framework, o.File, rawTitle))
		if o.AttemptID != nil {
			key += o.AttemptID.String()
		}
		isFailure := o.Status == retryFailed || o.Status == "timedOut" || o.Status == "interrupted" || o.Status == retryFlaky
		failed[key] = failed[key] || isFailure
		if index, ok := indices[key]; ok {
			previous := out[index]
			o.DurationMS += previous.DurationMS
			o.RetryCount = max(o.RetryCount, previous.RetryCount)
			if o.Message == "" {
				o.Message = previous.Message
			}
			if o.Stack == "" {
				o.Stack = previous.Stack
			}
			if o.Status == "skipped" {
				o.Status = previous.Status
			}
			if o.Status == "passed" && failed[key] {
				o.Status = retryFlaky
			}
			out[index] = o
		} else {
			indices[key] = len(out)
			out = append(out, o)
		}
	}
	return out
}
