package server

import (
	"context"
	"fmt"

	"github.com/99designs/gqlgen/graphql"

	"git.sr.ht/~sircmpwn/core-go/auth"
)

func AnonInternal(ctx context.Context, obj interface{},
	next graphql.Resolver) (interface{}, error) {

	if auth.ForContext(ctx).AuthMethod != auth.AUTH_ANON_INTERNAL {
		return nil, fmt.Errorf("Internal auth access denied")
	}

	return next(ctx)
}

func Internal(ctx context.Context, obj interface{},
	next graphql.Resolver) (interface{}, error) {

	if auth.ForContext(ctx).AuthMethod != auth.AUTH_INTERNAL {
		return nil, fmt.Errorf("Internal auth access denied")
	}

	return next(ctx)
}

func Private(ctx context.Context, obj interface{},
	next graphql.Resolver) (interface{}, error) {

	user := auth.ForContext(ctx)
	switch user.AuthMethod {
	case auth.AUTH_INTERNAL:
		return next(ctx)
	case auth.AUTH_OAUTH2:
		if user.BearerToken.ClientID != "" {
			return nil, fmt.Errorf("Private auth access denied")
		}
		return next(ctx)
	}

	return nil, fmt.Errorf("Private auth access denied")
}

func Access(ctx context.Context, obj interface{}, next graphql.Resolver,
	scope string, kind string) (interface{}, error) {
	authctx := auth.ForContext(ctx)

	switch authctx.AuthMethod {
	case auth.AUTH_INTERNAL, auth.AUTH_COOKIE:
		return next(ctx)
	case auth.AUTH_OAUTH_LEGACY:
		if kind == auth.RO {
			// Only legacy tokens with "*" scopes ever get this far
			return next(ctx)
		}
	case auth.AUTH_WEBHOOK:
		if kind != auth.RO {
			return nil, fmt.Errorf("Access to read/write resolver denied for webhook")
		}
		fallthrough
	case auth.AUTH_OAUTH2:
		if authctx.Grants.Has(scope, kind) {
			return next(ctx)
		}
	default:
		panic(fmt.Errorf("Unknown auth method for access check"))
	}

	return nil, fmt.Errorf("Access denied for invalid auth method")
}
