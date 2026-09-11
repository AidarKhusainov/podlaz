package daemon

import "context"

type networkSessionReplayContextKey struct{}

func withNetworkSessionReplayContext(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, networkSessionReplayContextKey{}, true)
}

func isNetworkSessionReplayContext(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	value, _ := ctx.Value(networkSessionReplayContextKey{}).(bool)
	return value
}
