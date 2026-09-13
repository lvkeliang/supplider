package aigateway

import "context"

// UsageSink receives a token-count record after a completed chat completion.
// The concrete tracker persists cumulative per-month counters (sampled via the
// supplier settings store); nil sink = no tracking. Recording is best-effort
// and never blocks or aborts the caller.
type UsageSink interface {
	Record(ctx context.Context, task string, tokensIn, tokensOut int)
}
