package config

import (
	"context"
	"errors"
	"net/http"

	"github.com/vaughan0/go-ini"
)

var configCtxKey = &contextKey{"config"}
var serviceCtxKey = &contextKey{"name"}

type contextKey struct {
	name string
}

func Middleware(conf ini.File, service string) func(next http.Handler) http.Handler {
	svc := service
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := context.WithValue(r.Context(), configCtxKey, conf)
			ctx = context.WithValue(ctx, serviceCtxKey, &svc)
			r = r.WithContext(ctx)
			next.ServeHTTP(w, r)
		})
	}
}

func ForContext(ctx context.Context) ini.File {
	raw, ok := ctx.Value(configCtxKey).(ini.File)
	if !ok {
		panic(errors.New("Invalid config context"))
	}
	return raw
}

func ServiceName(ctx context.Context) string {
	raw, ok := ctx.Value(serviceCtxKey).(*string)
	if !ok {
		panic(errors.New("Invalid config context"))
	}
	return *raw
}
