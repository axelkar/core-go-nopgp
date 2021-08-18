package webhooks

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"strings"
	"time"

	"git.sr.ht/~sircmpwn/dowork"
	"github.com/99designs/gqlgen/graphql"
	"github.com/google/uuid"
	sq "github.com/Masterminds/squirrel"

	"git.sr.ht/~sircmpwn/core-go/auth"
	"git.sr.ht/~sircmpwn/core-go/crypto"
	"git.sr.ht/~sircmpwn/core-go/database"
)

type WebhookQueue struct {
	Queue  *work.Queue
	Schema graphql.ExecutableSchema
}

type WebhookSubscription struct {
	ID        int
	URL       string
	Query     string
	TokenHash string
	Grants    string
	ClientID  *string
	Expires   time.Time
}

// Creates a new worker for delivering webhooks. The caller must start the
// worker themselves.
func NewQueue(schema graphql.ExecutableSchema) *WebhookQueue {
	return &WebhookQueue{work.NewQueue("webhooks"), schema}
}

// Schedules delivery of a webhook to a set of subscribers.
//
// The select builder should not return any columns, i.e. the caller should use
// squirrel.Select() with no parameters. The caller should prepare FROM and any
// WHERE clauses which are necessary to refine the subscriber list (e.g. by
// affected resource ID). The caller must alias the webhook table to "sub", e.g.
// sq.Select().From("my_webhook_subscription sub").
//
// Name shall be the prefix of the webhook tables, e.g. "profile" for
// "gql_profile_wh_{delivery,sub}".
//
// The context should NOT be the context used to service the HTTP request which
// initiated the webhook delivery. It should instead be a fresh background
// context which contains the necessary state for your application to process
// the webhook resolvers.
func (queue *WebhookQueue) Schedule(ctx context.Context, q sq.SelectBuilder,
	name, event string, payloadUUID uuid.UUID, payload interface{}) {
	user := auth.ForContext(ctx)
	// The following tasks are done during this process:
	//
	// 1. Fetch subscription details from the database
	// 2. Prepare deliveries and create delivery records
	// 3. Deliver the webhooks
	//
	// The first two steps are done in this task, then N tasks are created for
	// step 3 where N = number of subscriptions.
	task := work.NewTask(func(ctx context.Context) error {
		ctx = Context(ctx, payload)
		subs, err := queue.fetchSubscriptions(ctx, q, event)
		if err != nil {
			return err
		}
		if len(subs) == 0 {
			return nil
		}

		tasks := make([]*work.Task, len(subs))
		if err := database.WithTx(ctx, nil, func(tx *sql.Tx) error {
			var err error
			for i, sub := range subs {
				webhook := WebhookContext{
					Name:         name,
					Event:        event,
					User:         user,
					Payload:      payload,
					PayloadUUID:  payloadUUID,
					Subscription: sub,
				}
				tasks[i], err = queue.queueStage2(ctx, tx, &webhook)
				if err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			log.Printf("Failed to enqueue %s/%s webhooks: %v", name, event, err)
			return err
		}

		for _, task := range tasks {
			queue.Queue.Enqueue(task)
		}
		log.Printf("Enqueued %s/%s webhook delivery for %d subscriptions",
			name, event, len(subs))
		return nil
	})
	queue.Queue.Enqueue(task)
}

func (queue *WebhookQueue) fetchSubscriptions(ctx context.Context,
	q sq.SelectBuilder, event string) ([]*WebhookSubscription, error) {
	var subs []*WebhookSubscription
	if err := database.WithTx(ctx, &sql.TxOptions{
		Isolation: 0,
		ReadOnly: true,
	}, func(tx *sql.Tx) error {
		var (
			err  error
			rows *sql.Rows
		)
		if rows, err = q.
			Columns("sub.id", "sub.url", "sub.query",
				"sub.token_hash", "sub.grants", "sub.client_id",
				"sub.expires").
			Where("? = ANY(sub.events)", event).
			PlaceholderFormat(sq.Dollar).
			RunWith(tx).
			QueryContext(ctx); err != nil {
			return err
		}
		defer rows.Close()

		for rows.Next() {
			var sub WebhookSubscription
			if err := rows.Scan(&sub.ID, &sub.URL, &sub.Query,
				&sub.TokenHash, &sub.Grants, &sub.ClientID,
				&sub.Expires); err != nil {
				panic(err)
			}
			subs = append(subs, &sub)
		}

		return nil
	}); err != nil {
		return nil, err
	}
	return subs, nil
}

func (queue *WebhookQueue) queueStage2(ctx context.Context,
	tx *sql.Tx, webhook *WebhookContext) (*work.Task, error) {
	headers := make(http.Header)
	headers.Set("Content-Type", "application/json")
	headers.Set("X-Webhook-Event", webhook.Event)
	headers.Set("X-Webhook-Delivery", webhook.PayloadUUID.String())

	payload, err := webhook.Exec(ctx, queue.Schema)
	if err != nil {
		return nil, err
	}

	var deliveryID int
	err = sq.
		Insert("gql_"+webhook.Name+"_wh_delivery").
		Columns("uuid", "date", "event", "subscription_id", "request_body").
		Values(webhook.PayloadUUID, sq.Expr("NOW() at time zone 'utc'"),
			webhook.Event, webhook.Subscription.ID, string(payload)).
		Suffix(`RETURNING (id)`).
		PlaceholderFormat(sq.Dollar).
		RunWith(tx).
		ScanContext(ctx, &deliveryID)
	if err != nil {
		return nil, err
	}

	return work.NewTask(func(ctx context.Context) error {
		return queue.deliverPayload(ctx, webhook, headers, payload, deliveryID)
	}).Retries(5).After(func(ctx context.Context, task *work.Task) {
		if task.Result() == nil {
			log.Printf("%s: webhook delivery complete after %d attempts",
				webhook.PayloadUUID, task.Attempts())
		} else {
			log.Printf("%s: webhook delivery failed after %d attempts: %v",
				webhook.PayloadUUID, task.Attempts(), task.Result())
		}
	}), nil
}

// Performs a webhook delivery and updates the delivery record in the database
func (queue *WebhookQueue) deliverPayload(ctx context.Context,
	webhook *WebhookContext, headers http.Header, payload []byte,
	deliveryID int) error {

	client := &http.Client{
		Timeout: 30 * time.Second,
	}
	rctx, cancel := context.WithDeadline(ctx, time.Now().Add(30*time.Second))
	req, err := http.NewRequestWithContext(rctx,
		http.MethodPost, webhook.Subscription.URL, bytes.NewReader(payload))
	defer cancel()
	if err != nil {
		return fmt.Errorf("http.NewRequestWithContext: %v: %e",
			err, work.ErrDoNotReattempt)
	}

	req.Header = make(http.Header)
	for key, values := range headers {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
	nonce, sig := crypto.SignWebhook(payload)
	req.Header.Add("X-Payload-Nonce", nonce)
	req.Header.Add("X-Payload-Signature", sig)

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	reader := io.LimitReader(resp.Body, 262144) // No more than 256 KiB
	body, err := ioutil.ReadAll(reader)
	if err != nil {
		return fmt.Errorf("Error reading response body: %v: %e",
			err, work.ErrDoNotReattempt)
	}

	if err = database.WithTx(ctx, nil, func(tx *sql.Tx) error {
		var theirs strings.Builder
		resp.Header.Write(&theirs)
		_, err := sq.
			Update("gql_"+webhook.Name+"_wh_delivery").
			Set("response_body", string(body)).
			Set("response_status", resp.StatusCode).
			Set("response_headers", theirs.String()).
			Where("id = ?", deliveryID).
			PlaceholderFormat(sq.Dollar).
			RunWith(tx).
			ExecContext(ctx)
		return err
	}); err != nil {
		log.Printf("Warning: webhook delivered, but updating delivery record failed: %v", err)
		return nil
	}

	if resp.StatusCode == http.StatusBadGateway ||
		resp.StatusCode == http.StatusServiceUnavailable ||
		resp.StatusCode == http.StatusGatewayTimeout {
		// Retry
		return fmt.Errorf("Server returned status %d: %s",
			resp.StatusCode, resp.Status)
	}

	return nil
}
