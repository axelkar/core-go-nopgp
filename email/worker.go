package email

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"git.sr.ht/~sircmpwn/dowork"
	"github.com/emersion/go-message/mail"
	"github.com/emersion/go-pgpmail"
	"golang.org/x/crypto/openpgp"
	_ "github.com/emersion/go-message/charset"

	"git.sr.ht/~sircmpwn/core-go/config"
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

// Enqueues an email for sending with the default parameters.
func Enqueue(ctx context.Context, msg *bytes.Buffer, rcpts []string) {
	ForContext(ctx).Enqueue(NewTask(msg, rcpts))
}

// Updates an email with the standard SourceHut headers, signs and optionally
// encrypts it, and then queues it for delivery.
//
// Senders should fill in at least the To and Subject headers, and the message
// body. Message-ID, Date, From, and Reply-To will be added here.
func EnqueueStd(ctx context.Context, header mail.Header,
	bodyReader io.Reader, rcptKey *string) error {

	// XXX: Do we really need to load all this shit every time we send an email
	conf := config.ForContext(ctx)
	smtpFrom, ok := conf.Get("mail", "smtp-from")
	if !ok {
		panic(errors.New("Expected [mail]smtp-from in config"))
	}
	ownerName, ok := conf.Get("sr.ht", "owner-name")
	if !ok {
		panic(errors.New("Expected [sr.ht]owner-name in config"))
	}
	ownerEmail, ok := conf.Get("sr.ht", "owner-email")
	if !ok {
		panic(errors.New("Expected [sr.ht]owner-email in config"))
	}

	from, err := header.AddressList("From")
	if err != nil {
		return err
	}
	to, err := header.AddressList("To")
	if err != nil {
		return err
	}
	cc, err := header.AddressList("Cc")
	if err != nil {
		return err
	}

	if addr, err := mail.ParseAddress(smtpFrom); err == nil {
		from = append(from, addr)
	} else {
		panic(err)
	}

	var rcpts []string
	for _, addr := range to {
		rcpts = append(rcpts, addr.Address)
	}
	for _, addr := range cc {
		rcpts = append(rcpts, addr.Address)
	}

	header.GenerateMessageID()
	header.SetDate(time.Now().UTC())
	header.SetAddressList("From", from)
	header.Header.SetText("Reply-To",
		fmt.Sprintf("%s <%s>", ownerName, ownerEmail))

	privKeyPath, ok := conf.Get("mail", "pgp-privkey")
	if !ok {
		panic(errors.New("Expected [sr.ht]owner-email in config"))
	}
	privKeyFile, err := os.Open(privKeyPath)
	if err != nil {
		panic(err)
	}
	defer privKeyFile.Close()

	keyring, err := openpgp.ReadArmoredKeyRing(privKeyFile)
	if err != nil {
		panic(err)
	}
	if len(keyring) != 1 {
		panic(errors.New("Expected site PGP key to contain one key"))
	}
	entity := keyring[0]
	if entity.PrivateKey == nil || entity.PrivateKey.Encrypted {
		panic(errors.New("Failed to load private key for email signature"))
	}

	var (
		buf       bytes.Buffer
		cleartext io.WriteCloser
	)

	if rcptKey != nil {
		keyring, err = openpgp.ReadArmoredKeyRing(strings.NewReader(*rcptKey))
		if err != nil {
			log.Fatal(err)
		}
		if len(keyring) != 1 {
			return errors.New("Expected user PGP key to contain one key")
		}
		rcptEntity := keyring[0]

		cleartext, err = pgpmail.Encrypt(&buf, header.Header.Header,
			[]*openpgp.Entity{rcptEntity}, entity, nil)
		if err != nil {
			log.Fatal(err)
		}
		defer cleartext.Close()
	} else {
		cleartext, err = pgpmail.Sign(&buf, header.Header.Header,
			entity, nil)
		if err != nil {
			log.Fatal(err)
		}
		defer cleartext.Close()
	}

	var inlineHeader mail.Header
	inlineHeader.SetContentType("text/plain", nil)
	body, err := mail.CreateSingleInlineWriter(cleartext, inlineHeader)
	if err != nil {
		panic(err)
	}
	defer body.Close()

	_, err = io.Copy(body, bodyReader)
	if err != nil {
		log.Fatal(err)
	}

	Enqueue(ctx, &buf, rcpts)
	return nil
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

// Returns a context which includes the given mail worker.
func Context(ctx context.Context, queue *work.Queue) context.Context {
	return context.WithValue(ctx, emailCtxKey, queue)
}

// Adds HTTP middleware to provide an email work queue to this context.
func Middleware(queue *work.Queue) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r = r.WithContext(Context(r.Context(), queue))
			next.ServeHTTP(w, r)
		})
	}
}
