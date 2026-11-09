package tool

import "context"

// Health is one observation of a target after an applied write.
type Health struct {
	// Done means the result will not change by waiting: the target is
	// healthy, or it failed in a way that needs another action.
	Done bool
	// Healthy means the target serves as intended.
	Healthy bool
	// Detail is a short, fixed-format description such as "ready 2/2".
	Detail string
}

// Check observes the target of an applied write. Approval pages poll it to
// tell the person whether the action restored the service, not only whether
// the API accepted the change.
type Check func(ctx context.Context) (Health, error)

type checkSinkKey struct{}

// WithCheckSink returns a context in which a successful approved write hands
// its plan's Check to sink. Callers without a sink ignore checks.
func WithCheckSink(ctx context.Context, sink func(Check)) context.Context {
	return context.WithValue(ctx, checkSinkKey{}, sink)
}

// reportCheck passes an applied plan's check to the context's sink, if any.
func reportCheck(ctx context.Context, p Plan) {
	if p.Check == nil {
		return
	}
	if sink, ok := ctx.Value(checkSinkKey{}).(func(Check)); ok && sink != nil {
		sink(p.Check)
	}
}
