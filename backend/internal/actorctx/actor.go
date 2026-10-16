// Package actorctx carries a verified application actor across layers without
// importing HTTP middleware into the repository. Request bodies cannot set it.
package actorctx

import "context"

type actorKey struct{}

func WithUserID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, actorKey{}, id)
}
func UserID(ctx context.Context) string { id, _ := ctx.Value(actorKey{}).(string); return id }
