package modules

import "context"

type taskAuthKey struct{}

// WithTaskAuth attaches a task authorization credential to ctx for module calls.
func WithTaskAuth(ctx context.Context, credential string) context.Context {
	return context.WithValue(ctx, taskAuthKey{}, credential)
}

// TaskAuth returns the task authorization credential on ctx, if any.
func TaskAuth(ctx context.Context) string {
	s, _ := ctx.Value(taskAuthKey{}).(string)
	return s
}
