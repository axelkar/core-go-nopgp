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
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := Context(r.Context(), conf, service)
			r = r.WithContext(ctx)
			next.ServeHTTP(w, r)
		})
	}
}

func Context(ctx context.Context, conf ini.File, service string) context.Context {
	svc := service
	ctx = context.WithValue(ctx, configCtxKey, conf)
	ctx = context.WithValue(ctx, serviceCtxKey, &svc)
	return ctx
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
		panic(errors.New("Invalid service config context"))
	}
	return *raw
}
