package email

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	work "git.sr.ht/~sircmpwn/dowork"
	_ "github.com/emersion/go-message/charset"
	"github.com/emersion/go-message/mail"
	"github.com/vaughan0/go-ini"
)

var emailCtxKey = &contextKey{"email"}

type contextKey struct {
	name string
}

// Returns a task which will send this email for the work queue. If the caller
// does not need to customize the task parameters, the Enqueue function may be
// more desirable.
func NewTask(msg *bytes.Buffer, rcpts []string) *work.Task {
	return work.NewTask(func(ctx context.Context) error {
		err := Send(ctx, msg, rcpts)
		if err != nil {
			log.Printf("Error sending mail: %v", err)
		}
		return err
	}).Retries(10).After(func(ctx context.Context, task *work.Task) {
		if task.Result() == nil {
			log.Printf("Mail to %s sent after %d attempts",
				strings.Join(rcpts, ", "), task.Attempts())
		} else {
			log.Printf("Mail to %s failed after %d attempts: %v",
				strings.Join(rcpts, ", "), task.Attempts(), task.Result())
		}
	})
}

// Updates an email with the standard SourceHut headers and then queues it for delivery.
//
// Senders should fill in at least the To and Subject headers, and the message
// body. Message-ID, Date, From, and Reply-To will also be added if they are not
// already present.
func EnqueueStd(ctx context.Context, header mail.Header,
	bodyReader io.Reader, rcptKey *string) error {

	queue := ForContext(ctx)

	to, err := header.AddressList("To")
	if err != nil {
		return fmt.Errorf("invalid To header field: %v", err)
	}
	cc, err := header.AddressList("Cc")
	if err != nil {
		return fmt.Errorf("invalid Cc header field: %v", err)
	}

	var rcpts []string
	for _, addr := range to {
		rcpts = append(rcpts, addr.Address)
	}
	for _, addr := range cc {
		rcpts = append(rcpts, addr.Address)
	}

	// Disallow content headers, signing/encrypting will change this
	header.Del("Content-Transfer-Encoding")
	header.Del("Content-Type")
	header.Del("Content-Disposition")

	if !header.Has("Message-Id") {
		header.GenerateMessageID()
	}
	if !header.Has("Date") {
		header.SetDate(time.Now().UTC())
	}
	if !header.Has("From") {
		header.SetAddressList("From", []*mail.Address{queue.smtpFrom})
	}
	if !header.Has("Reply-To") {
		header.SetAddressList("Reply-To", []*mail.Address{queue.ownerAddress})
	}

	var (
		buf       bytes.Buffer
	)

	header.SetContentType("text/plain", nil)
	body, err := mail.CreateSingleInlineWriter(&buf, header)
	if err != nil {
		panic(err)
	}
	defer body.Close()

	_, err = io.Copy(body, bodyReader)
	if err != nil {
		log.Fatal(err)
	}

	return queue.Enqueue(NewTask(&buf, rcpts))
}

type Queue struct {
	*work.Queue
	smtpFrom     *mail.Address
	ownerAddress *mail.Address
}

// Creates a new email processing queue.
func NewQueue(conf ini.File) *Queue {
	smtpFrom, ok := conf.Get("mail", "smtp-from")
	if !ok {
		panic("Expected [mail]smtp-from in config")
	}
	ownerName, ok := conf.Get("sr.ht", "owner-name")
	if !ok {
		panic("Expected [sr.ht]owner-name in config")
	}
	ownerEmail, ok := conf.Get("sr.ht", "owner-email")
	if !ok {
		panic("Expected [sr.ht]owner-email in config")
	}
	addr, err := mail.ParseAddress(smtpFrom)
	if err != nil {
		panic(err)
	}
	ownerAddr := &mail.Address{
		Name:    ownerName,
		Address: ownerEmail,
	}

	return &Queue{
		Queue:        work.NewQueue("email"),
		smtpFrom:     addr,
		ownerAddress: ownerAddr,
	}
}

// Returns the email worker for this context.
func ForContext(ctx context.Context) *Queue {
	q, ok := ctx.Value(emailCtxKey).(*Queue)
	if !ok {
		panic(errors.New("No email worker for this context"))
	}
	return q
}

// Returns a context which includes the given mail worker.
func Context(ctx context.Context, queue *Queue) context.Context {
	return context.WithValue(ctx, emailCtxKey, queue)
}

// Adds HTTP middleware to provide an email work queue to this context.
func Middleware(queue *Queue) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r = r.WithContext(Context(r.Context(), queue))
			next.ServeHTTP(w, r)
		})
	}
}
