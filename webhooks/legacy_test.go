package webhooks

import (
	"context"
	"database/sql/driver"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	sq "github.com/Masterminds/squirrel"
	"github.com/stretchr/testify/assert"
	"github.com/vaughan0/go-ini"

	"git.sr.ht/~sircmpwn/core-go/crypto"
	"git.sr.ht/~sircmpwn/core-go/database"
)

func init() {
	conf, err := ini.Load(strings.NewReader(`
[webhooks]
private-key=ebzsjPaN6E13ln/FeNWly1C92q6bVMVdOnDo1HPl5fc=

[sr.ht]
network-key=tbuG-7Vh44vrDq1L_HKWkHnWrDOtJhEkPKPiauaLeuk=

[test::api]
internal-ipnet=127.0.0.1/24,::1/64`))
	if err != nil {
		panic(err)
	}
	crypto.InitCrypto(conf)
}

type argContains struct {
	matches []string
}

func ArgMatchesAll(matches ...string) *argContains {
	return &argContains{matches}
}

func (ac *argContains) Match(v driver.Value) bool {
	str, ok := v.(string)
	if !ok {
		return false
	}
	for _, match := range ac.matches {
		if !strings.Contains(str, match) {
			return false
		}
	}
	return true
}

func TestDelivery(t *testing.T) {
	var called bool
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			defer r.Body.Close()

			called = true
			assert.Equal(t, r.Method, http.MethodPost)
			assert.Equal(t, r.URL.Path, "/webhook")

			assert.NotEqual(t, "", r.Header.Get("X-Webhook-Delivery"))
			assert.Equal(t, "profile:update", r.Header.Get("X-Webhook-Event"))
			assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

			b, err := ioutil.ReadAll(r.Body)
			assert.Nil(t, err)
			assert.Equal(t, `{"hello": "world"}`, string(b))

			nonce := r.Header.Get("X-Payload-Nonce")
			signature := r.Header.Get("X-Payload-Signature")
			assert.True(t, crypto.VerifyWebhook(b, nonce, signature))

			w.Write([]byte("Thanks!"))
		}))
	defer srv.Close()

	queue := NewLegacyQueue()
	q := sq.
		Select().
		From("user_webhook_subscription sub").
		Where(`sub.user_id = ?`, 42)
	queue.Schedule(q, "user", "profile:update", []byte(`{"hello": "world"}`))

	db, mock, err := sqlmock.New()
	if err != nil {
		panic(err)
	}

	// Lookup phase
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT .* FROM user_webhook_subscription sub`).
		WillReturnRows(sqlmock.NewRows([]string{
			"sub.id", "sub.created", "sub.url", "sub.events",
		}).AddRow(1337, time.Now().UTC(),
						srv.URL+"/webhook", "profile:update")).
		WithArgs(42, sqlmock.AnyArg()) // Any => events LIKE %profile:update%
	mock.ExpectCommit()

	// Schedule phase
	mock.ExpectBegin()
	mock.ExpectQuery(`INSERT INTO user_webhook_delivery`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(4096))
	mock.ExpectCommit()

	ctx := database.Context(context.Background(), db)
	queue.Queue.Dispatch(ctx)

	assert.Nil(t, mock.ExpectationsWereMet())

	// Delivery phase
	db, mock, err = sqlmock.New()
	if err != nil {
		panic(err)
	}

	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE user_webhook_delivery`).
		WithArgs("Thanks!", 200,
			sqlmock.AnyArg(), // Response headers
			ArgMatchesAll(
				"X-Payload-Signature",
				"X-Payload-Nonce",
				"X-Webhook-Event",
				"X-Webhook-Delivery",
			), // Final request headers
			4096).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	ctx = database.Context(context.Background(), db)
	queue.Queue.Dispatch(ctx)

	assert.Nil(t, mock.ExpectationsWereMet())
	assert.True(t, called)
}
