package redis

import (
	"context"
	"errors"
	"net/http"

	goRedis "github.com/go-redis/redis/v8"
)

var redisCtxKey = &contextKey{"redis"}

type contextKey struct {
	name string
}

func Middleware(client *goRedis.Client) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := context.WithValue(r.Context(), redisCtxKey, client)

			r = r.WithContext(ctx)
			next.ServeHTTP(w, r)
		})
	}
}

func ForContext(ctx context.Context) *goRedis.Client {
	raw, ok := ctx.Value(redisCtxKey).(*goRedis.Client)
	if !ok {
		panic(errors.New("Invalid redis context"))
	}
	return raw
}
