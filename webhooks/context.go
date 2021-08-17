package webhooks

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"git.sr.ht/~sircmpwn/core-go/auth"
)

type contextKey struct {
	name string
}

var payloadContextKey = &contextKey{"webhookPayloadContext"}

type WebhookContext struct {
	Name         string
	Event        string
	User         *auth.AuthContext
	Payload      interface{}
	PayloadUUID  uuid.UUID
	Subscription *WebhookSubscription
}

// Prepares an context for a specific webhook delivery.
func Context(ctx context.Context, payload interface{}) context.Context {
	return context.WithValue(ctx, payloadContextKey, payload)
}

// Returns the active payload for a webhook context.
func Payload(ctx context.Context) (interface{}, error) {
	payload := ctx.Value(payloadContextKey)
	if payload == nil {
		return nil, errors.New("Cannot use this resolver without an active webhook context")
	}
	return payload, nil
}
