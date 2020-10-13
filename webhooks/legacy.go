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
	"github.com/google/uuid"
	sq "github.com/Masterminds/squirrel"

	"git.sr.ht/~sircmpwn/core-go/crypto"
	"git.sr.ht/~sircmpwn/core-go/database"
)

type LegacyQueue struct {
	Queue *work.Queue
}

type LegacySubscription struct {
	ID      int
	Created time.Time
	URL     string
	Events  []string
}

// Creates a new worker for delivering legacy webhooks. The caller must start
// the worker themselves.
func NewLegacyQueue() *LegacyQueue {
	return &LegacyQueue{
		work.NewQueue("webhooks_legacy"),
	}
}

// Schedules delivery of a legacy webhook to a set of subscribers.
//
// The select builder should not return any columns, i.e. the caller should use
// squirrel.Select() with no parameters. The caller should prepare FROM and any
// WHERE clauses which are necessary to refine the subscriber list (e.g. by
// affected resource ID). The caller must alias the webhook table to "sub", e.g.
// sq.Select().From("my_webhook_subscription sub").
//
// Name shall be the prefix of the webhook tables, e.g. "user" for
// "user_webhook_{delivery,subscription}".
func (lq *LegacyQueue) Schedule(q sq.SelectBuilder,
	name, event string, payload []byte) {
	// The following tasks are done during this process:
	//
	// 1. Fetch subscription details from the database
	// 2. Prepare deliveries and create delivery records
	// 3. Deliver the webhooks
	//
	// The first two steps are done in this task, then N tasks are created for
	// step 3 where N = number of subscriptions.
	task := work.NewTask(func(ctx context.Context) error {
		subs, err := fetchSubscriptions(ctx, q, event)
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
				tasks[i], err = lq.queueStage2(ctx, tx,
					name, event, sub, payload)
				if err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			log.Printf("Failed to enqueue webhooks: %v", err)
			return err
		}

		for _, task := range tasks {
			lq.Queue.Enqueue(task)
		}
		log.Printf("Enqueued %s %s webhook delivery for %d subscriptions",
			name, event, len(subs))
		return nil
	})
	lq.Queue.Enqueue(task)
}

func fetchSubscriptions(ctx context.Context, q sq.SelectBuilder,
	event string) ([]*LegacySubscription, error) {

	var subs []*LegacySubscription
	if err := database.WithTx(ctx, &sql.TxOptions{
		Isolation: 0,
		ReadOnly:  true,
	}, func(tx *sql.Tx) error {
		var (
			err  error
			rows *sql.Rows
		)
		if rows, err = q.
			Columns("sub.id", "sub.created", "sub.url", "sub.events").
			Where(sq.Like{"sub.events": "%" + event + "%"}).
			PlaceholderFormat(sq.Dollar).
			RunWith(tx).
			QueryContext(ctx); err != nil {
			return err
		}
		defer rows.Close()

		var events string
		for rows.Next() {
			var sub LegacySubscription
			if err := rows.Scan(&sub.ID, &sub.Created,
				&sub.URL, &events); err != nil {
				panic(err)
			}

			// The LIKE clause gets us an approximate list of implicated
			// subscriptions, so we quickly decode the event list and
			// double check here to get the final list.
			sub.Events = strings.Split(events, ",")

			var valid bool
			for _, e := range sub.Events {
				if e == event {
					valid = true
					break
				}
			}

			if valid {
				subs = append(subs, &sub)
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}

	return subs, nil
}

// Inserts the delivery record and schedules the actual delivery task
func (lq *LegacyQueue) queueStage2(ctx context.Context, tx *sql.Tx,
	name, event string, sub *LegacySubscription,
	payload []byte) (*work.Task, error) {

	deliveryUUID := uuid.New().String()
	headers := make(http.Header)
	headers.Set("Content-Type", "application/json")
	headers.Set("X-Webhook-Event", event)
	headers.Set("X-Webhook-Delivery", deliveryUUID)
	var sb strings.Builder
	headers.Write(&sb)

	var deliveryID int
	err := sq.
		Insert(name+"_webhook_delivery").
		Columns("uuid", "created", "event", "url",
			"payload", "payload_headers", "response_status",
			"subscription_id").
		Values(deliveryUUID,
			sq.Expr("NOW() at time zone 'utc'"),
			event, sub.URL, string(payload), sb.String(), -2, sub.ID).
		Suffix(`RETURNING (id)`).
		PlaceholderFormat(sq.Dollar).
		RunWith(tx).
		ScanContext(ctx, &deliveryID)
	if err != nil {
		return nil, err
	}

	return work.NewTask(func(ctx context.Context) error {
		return deliverPayload(ctx, name, sub.URL, headers, payload, deliveryID)
	}).Retries(5).After(func(ctx context.Context, task *work.Task) {
		if task.Result() == nil {
			log.Printf("%s: delivery complete after %d attempts",
				deliveryUUID, task.Attempts())
		} else {
			log.Printf("%s: delivery failed after %d attempts: %v",
				deliveryUUID, task.Attempts(), task.Result())
		}
	}), nil
}

// Performs a webhook delivery and updates the delivery record in the database
func deliverPayload(ctx context.Context, name, url string,
	headers http.Header, payload []byte, deliveryID int) error {

	client := &http.Client{
		Timeout: 30 * time.Second,
	}
	rctx, cancel := context.WithDeadline(ctx, time.Now().Add(30*time.Second))
	req, err := http.NewRequestWithContext(rctx,
		http.MethodPost, url, bytes.NewReader(payload))
	defer cancel()
	if err != nil {
		return fmt.Errorf("http.NewRequestWithContext: %v: %e",
			err, work.ErrDoNotReattempt)
	}

	req.Header = headers
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

	reader := io.LimitReader(resp.Body, 65536) // No more than 64 KiB
	body, err := ioutil.ReadAll(reader)
	if err != nil {
		return fmt.Errorf("Error reading response body: %v: %e",
			err, work.ErrDoNotReattempt)
	}

	if err = database.WithTx(ctx, nil, func(tx *sql.Tx) error {
		var sb strings.Builder
		resp.Header.Write(&sb)
		_, err := sq.
			Update(name+"_webhook_delivery").
			Set("response", string(body)).
			Set("response_status", resp.StatusCode).
			Set("response_headers", sb.String()).
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
