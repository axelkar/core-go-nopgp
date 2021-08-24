package webhooks

import (
	"context"
	"encoding/hex"
	"fmt"
	"time"

	"git.sr.ht/~sircmpwn/core-go/auth"
)

// The following invariants apply to AuthConfig:
// 1. AuthMethod will be either OAUTH2 or INTERNAL
// 2. If OAUTH2, TokenHash, Grants, and Expires will be non-nil, and ClientID
//    may be non-nil, and NodeID will be nil.
// 3. If INTERNAL, TokenHash, Grants, Expires, and ClientID will be nil, and
//    NodeID will be non-nil.
type AuthConfig struct {
	AuthMethod string
	TokenHash  *string
	Grants     *string
	ClientID   *string
	Expires    *time.Time
	NodeID     *string
}

// Pulls auth details out of the config context and returns a structure of all
// of the information necessary to build a webhook context with the same
// authentication parameters.
func NewAuthConfig(ctx context.Context) (AuthConfig, error) {
	user := auth.ForContext(ctx)
	switch user.AuthMethod {
	case auth.AUTH_OAUTH_LEGACY:
		return AuthConfig{}, fmt.Errorf("Native webhooks are not supported with legacy OAuth")
	case auth.AUTH_OAUTH2:
		tokenHash := hex.EncodeToString(user.TokenHash[:])
		grants := user.BearerToken.Grants
		expires := user.BearerToken.Expires.Time()
		var clientID *string
		if user.BearerToken.ClientID != "" {
			_clientID := user.BearerToken.ClientID
			clientID = &_clientID
		}
		return AuthConfig {
			AuthMethod: user.AuthMethod,
			TokenHash:  &tokenHash,
			Grants:     &grants,
			Expires:    &expires,
			ClientID:   clientID,
		}, nil
	case auth.AUTH_COOKIE:
		// TODO: Should this work?
		return AuthConfig{}, fmt.Errorf("Native webhooks are not supported with web authentication")
	case auth.AUTH_INTERNAL:
		// TODO: Should this work?
		panic("Internal webtoken auth is not supported")
	case auth.AUTH_WEBHOOK:
		panic("Recursive webhook auth is not supported")
	}
	panic("Unreachable")
}
