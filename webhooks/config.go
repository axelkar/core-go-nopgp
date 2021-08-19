package webhooks

import (
	"context"
	"encoding/hex"
	"fmt"
	"time"

	"git.sr.ht/~sircmpwn/core-go/auth"
)

type AuthConfig struct {
	TokenHash string
	Grants    string
	ClientID  *string
	Expires   time.Time
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
		ac := AuthConfig{
			TokenHash: hex.EncodeToString(user.TokenHash[:]),
			Grants:    user.BearerToken.Grants,
			Expires:   user.BearerToken.Expires.Time(),
		}
		if user.BearerToken.ClientID != "" {
			clientID := user.BearerToken.ClientID
			ac.ClientID = &clientID
		}
		return ac, nil
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
