package server

import (
	"context"
	"fmt"

	"github.com/99designs/gqlgen/graphql"

	"git.sr.ht/~sircmpwn/core-go/auth"
)

func Internal(ctx context.Context, obj interface{},
	next graphql.Resolver) (interface{}, error) {

	if auth.ForContext(ctx).AuthMethod != auth.AUTH_INTERNAL {
		return nil, fmt.Errorf("Access denied")
	}

	return next(ctx)
}

func Access(ctx context.Context, obj interface{}, next graphql.Resolver,
	scope string, kind string) (interface{}, error) {

	authctx := auth.ForContext(ctx)

	switch authctx.AuthMethod {
	case auth.AUTH_INTERNAL:
	case auth.AUTH_COOKIE:
		return next(ctx)
	case auth.AUTH_OAUTH_LEGACY:
		if kind == "RO" {
			// Only legacy tokens with "*" scopes ever get this far
			return next(ctx)
		}
	case auth.AUTH_OAUTH2:
		if authctx.Access == nil {
			return next(ctx)
		}
		if access, ok := authctx.Access[scope]; !ok {
			break
		} else if access == "RO" && kind == "RW" {
			break
		}
		return next(ctx)
	default:
		panic(fmt.Errorf("Unknown auth method for access check"))
	}

	return nil, fmt.Errorf("Access denied")
}
