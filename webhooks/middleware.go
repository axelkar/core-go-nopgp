package webhooks

import (
	"context"
	"net/http"
)

var ctxKey = &contextKey{"webhooks"}

func Middleware(queue *WebhookQueue) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := context.WithValue(r.Context(), ctxKey, queue)
			r = r.WithContext(ctx)
			next.ServeHTTP(w, r)
		})
	}
}

func ForContext(ctx context.Context) *WebhookQueue {
	queue, ok := ctx.Value(ctxKey).(*WebhookQueue)
	if !ok {
		panic("No webhook queue for this context")
	}
	return queue
}

var legacyCtxKey = &contextKey{"legacy"}

func LegacyMiddleware(queue *LegacyQueue) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := context.WithValue(r.Context(), legacyCtxKey, queue)
			r = r.WithContext(ctx)
			next.ServeHTTP(w, r)
		})
	}
}

func LegacyForContext(ctx context.Context) *LegacyQueue {
	queue, ok := ctx.Value(legacyCtxKey).(*LegacyQueue)
	if !ok {
		panic("No legacy webhook queue for this context")
	}
	return queue
}
