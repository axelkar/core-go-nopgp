package webhooks

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/99designs/gqlgen/graphql"
	"github.com/99designs/gqlgen/graphql/executor"
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

// Executes the GraphQL query prepared stored in the WebhookContext. Handles
// the configuration of a secondary authentication and GraphQL context.
func (webhook *WebhookContext) Exec(ctx context.Context,
	schema graphql.ExecutableSchema) ([]byte, error) {
	sub := webhook.Subscription
	tslice, err := hex.DecodeString(sub.TokenHash)
	if err != nil {
		panic(err)
	}

	var tokenHash [64]byte
	copy(tokenHash[:], tslice)
	ctx, err = auth.WebhookAuth(ctx, webhook.User,
		tokenHash, sub.Grants, sub.ClientID, sub.Expires)
	if err != nil {
		// TODO: This codepath can occur when the token has expired, and we may
		// want to communicate this to the user.
		return nil, err
	}

	exec := executor.New(schema)
	params := graphql.RawParams{
		Query: sub.Query,
		ReadTime: graphql.TraceTiming{
			Start: graphql.Now(),
			End:   graphql.Now(),
		},
	}
	ctx = graphql.StartOperationTrace(ctx)
	rc, errors := exec.CreateOperationContext(ctx, &params)
	if errors != nil {
		panic(errors)
	}
	ctx = graphql.WithOperationContext(ctx, rc)
	var resp graphql.ResponseHandler
	resp, ctx = exec.DispatchOperation(ctx, rc)
	payload, err := json.Marshal(resp(ctx))
	if err != nil {
		panic(err)
	}
	return payload, nil
}
