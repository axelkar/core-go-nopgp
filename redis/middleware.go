package redis

import (
	"context"
	"net/http"

	"github.com/go-redis/redis/v8"
)

var redisCtxKey = &contextKey{"redis"}

type contextKey struct {
	name string
}

func Middleware(client *redis.Client) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := Context(r.Context(), client)
			r = r.WithContext(ctx)
			next.ServeHTTP(w, r)
		})
	}
}

func Context(ctx context.Context, client *redis.Client) context.Context {
	return context.WithValue(ctx, redisCtxKey, client)
}

func ForContext(ctx context.Context) *redis.Client {
	raw, ok := ctx.Value(redisCtxKey).(*redis.Client)
	if !ok {
		panic("Invalid redis context")
	}
	return raw
}
