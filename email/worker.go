package email

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strings"

	"git.sr.ht/~sircmpwn/dowork"
	gomail "gopkg.in/mail.v2"

	"git.sr.ht/~sircmpwn/core-go/config"
)

var emailCtxKey = &contextKey{"email"}

type contextKey struct {
	name string
}

// Returns a task which will send this email for the work queue. If the caller
// does not need to customize the task parameters, the Enqueue function may be
// more desirable.
func NewTask(ctx context.Context, m *gomail.Message) *work.Task {
	conf := config.ForContext(ctx)
	return work.NewTask(func(ctx context.Context) error {
		return Send(config.Context(ctx, conf), m)
	}).Retries(10).After(func(ctx context.Context, task *work.Task) {
		if task.Result() == nil {
			log.Printf("MAIL TO %s: '%s' sent after %d attempts",
				strings.Join(m.GetHeader("To"), ";"),
				strings.Join(m.GetHeader("Subject"), ";"),
				task.Attempts())
		} else {
			log.Printf("MAIL TO %s: '%s' failed after %d attempts: %v",
				strings.Join(m.GetHeader("To"), ";"),
				strings.Join(m.GetHeader("Subject"), ";"),
				task.Attempts(), task.Result())
		}
	})
}

// Enqueues an email for sending with the default parameters.
func Enqueue(ctx context.Context, m *gomail.Message) {
	ForContext(ctx).Enqueue(NewTask(ctx, m))
}

// Creates a new email processing queue.
func NewQueue() *work.Queue {
	return work.NewQueue("email")
}

// Returns the email worker for this context.
func ForContext(ctx context.Context) *work.Queue {
	q, ok := ctx.Value(emailCtxKey).(*work.Queue)
	if !ok {
		panic(errors.New("No email worker for this context"))
	}
	return q
}

// Adds HTTP middleware to provide an email work queue to this context.
func Middleware(worker *work.Queue) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := context.WithValue(r.Context(), emailCtxKey, worker)
			r = r.WithContext(ctx)
			next.ServeHTTP(w, r)
		})
	}
}
