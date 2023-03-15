package s3

import (
	"context"
	"net/http"

	"github.com/minio/minio-go/v7"
)

var minioCtxKey = &contextKey{"minio"}

type contextKey struct {
	name string
}

func Middleware(client *minio.Client) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := Context(r.Context(), client)
			r = r.WithContext(ctx)
			next.ServeHTTP(w, r)
		})
	}
}

func Context(ctx context.Context, client *minio.Client) context.Context {
	return context.WithValue(ctx, minioCtxKey, client)
}

func ForContext(ctx context.Context) *minio.Client {
	raw, ok := ctx.Value(minioCtxKey).(*minio.Client)
	if !ok {
		panic("Invalid minio context")
	}
	return raw
}
