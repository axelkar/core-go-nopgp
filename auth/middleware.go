package auth

import (
	"context"
	"crypto/sha512"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/vaughan0/go-ini"
	"github.com/vektah/gqlparser/gqlerror"

	"git.sr.ht/~sircmpwn/core-go/client"
	"git.sr.ht/~sircmpwn/core-go/config"
	"git.sr.ht/~sircmpwn/core-go/crypto"
	"git.sr.ht/~sircmpwn/core-go/database"
)

var userCtxKey = &contextKey{"user"}

type contextKey struct {
	name string
}

var (
	oauthBearerRegex  = regexp.MustCompile(`^[0-9a-f]{32}$`)
	oauth2BearerRegex = regexp.MustCompile(`^[0-9a-zA-Z_+/]{33,}$`)
)

const (
	USER_UNCONFIRMED       = "unconfirmed"
	USER_ACTIVE_NON_PAYING = "active_non_paying"
	USER_ACTIVE_FREE       = "active_free"
	USER_ACTIVE_PAYING     = "active_paying"
	USER_ACTIVE_DELINQUENT = "active_delinquent"
	USER_ADMIN             = "admin"
	USER_UNKNOWN           = "unknown"
	USER_SUSPENDED         = "suspended"
)

const (
	AUTH_OAUTH_LEGACY  = "OAUTH_LEGACY"
	AUTH_OAUTH2        = "OAUTH2"
	AUTH_COOKIE        = "COOKIE"
	AUTH_INTERNAL      = "INTERNAL"
	AUTH_ANON_INTERNAL = "ANON_INTERNAL"
	AUTH_WEBHOOK       = "WEBHOOK"
)

type AuthContext struct {
	AuthMethod       string

	// Only filled out for non-anonymous authentication
	UserID           int
	Created          time.Time
	Updated          time.Time
	Username         string
	Email            string
	UserType         string
	URL              *string
	Location         *string
	Bio              *string
	SuspensionNotice *string

	// Only set for meta.sr.ht-api
	PGPKey *string

	// Only filled out if AuthMethod == AUTH_INTERNAL
	InternalAuth InternalAuth

	// Only filled out if AuthMethod == AUTH_OAUTH2 or AUTH_WEBHOOK
	BearerToken *BearerToken
	Grants      Grants
	TokenHash   [64]byte
}

func authError(w http.ResponseWriter, reason string, code int) {
	gqlerr := gqlerror.Errorf("Authentication error: %s", reason)
	b, err := json.Marshal(struct {
		Errors []*gqlerror.Error `json:"errors"`
	}{
		Errors: []*gqlerror.Error{gqlerr},
	})
	if err != nil {
		panic(err)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	w.Write(b)
}

func authForUsername(ctx context.Context, username string) (*AuthContext, error) {
	var auth AuthContext
	if err := database.WithTx(ctx, &sql.TxOptions{
		Isolation: 0,
		ReadOnly:  true,
	}, func(tx *sql.Tx) error {
		var (
			err  error
			rows *sql.Rows
		)
		query := database.
			Select(ctx, []string{
				`u.id`, `u.username`,
				`u.created`, `u.updated`,
				`u.email`,
				`u.user_type`,
				`u.url`, `u.location`, `u.bio`,
				`u.suspension_notice`,
			}).
			From(`"user" u`).
			Where(`u.username = ?`, username)
		if rows, err = query.RunWith(tx).Query(); err != nil {
			panic(err)
		}
		defer rows.Close()

		if !rows.Next() {
			if err := rows.Err(); err != nil {
				panic(err)
			}
			return fmt.Errorf("Authenticating for unknown user %s", username)
		}
		if err := rows.Scan(&auth.UserID, &auth.Username, &auth.Created,
			&auth.Updated, &auth.Email, &auth.UserType, &auth.URL, &auth.Location,
			&auth.Bio, &auth.SuspensionNotice); err != nil {
			panic(err)
		}
		if rows.Next() {
			if err := rows.Err(); err != nil {
				panic(err) // Invariant
			}
			panic(errors.New("Multiple matching user accounts; invariant broken"))
		}
		return nil
	}); err != nil {
		return nil, err
	}

	if auth.UserType == USER_SUSPENDED {
		return nil, fmt.Errorf(
			"Account suspended with the following notice: %s\nContact support",
			*auth.SuspensionNotice)
	}

	return &auth, nil
}

// NOTE: This only works for meta.sr.ht (should we move it?)
func authForOAuthClient(ctx context.Context, clientUUID string) (*AuthContext, error) {
	var auth AuthContext
	if err := database.WithTx(ctx, &sql.TxOptions{
		Isolation: 0,
		ReadOnly:  true,
	}, func(tx *sql.Tx) error {
		var (
			err  error
			rows *sql.Rows
		)
		query := database.
			Select(ctx, []string{
				`u.id`, `u.username`,
				`u.created`, `u.updated`,
				`u.email`,
				`u.user_type`,
				`u.url`, `u.location`, `u.bio`,
				`u.suspension_notice`,
			}).
			From(`"oauth2_client" client`).
			Join(`"user" u ON u.id = client.owner_id`).
			Where(`client.client_uuid = ?`, clientUUID).
			Where(`client.revoked = false`)
		if rows, err = query.RunWith(tx).Query(); err != nil {
			panic(err)
		}
		defer rows.Close()

		if !rows.Next() {
			if err := rows.Err(); err != nil {
				panic(err)
			}
			return fmt.Errorf("Authenticating for unknown client ID %s", clientUUID)
		}
		if err := rows.Scan(&auth.UserID, &auth.Username, &auth.Created,
			&auth.Updated, &auth.Email, &auth.UserType, &auth.URL, &auth.Location,
			&auth.Bio, &auth.SuspensionNotice); err != nil {
			panic(err)
		}
		if rows.Next() {
			// TODO: Fetch user info from meta if necessary
			if err := rows.Err(); err != nil {
				panic(err)
			}
			panic(errors.New("Multiple matching user accounts; invariant broken"))
		}
		return nil
	}); err != nil {
		return nil, err
	}

	if auth.UserType == USER_SUSPENDED {
		return nil, fmt.Errorf(
			"Account suspended with the following notice: %s\nContact support",
			*auth.SuspensionNotice)
	}

	return &auth, nil
}

type AuthCookie struct {
	// The username of the authenticated user
	Name string `json:"name"`
}

func cookieAuth(cookie *http.Cookie, w http.ResponseWriter,
	r *http.Request, next http.Handler) {
	payload := crypto.DecryptWithoutExpiration([]byte(cookie.Value))
	if payload == nil {
		authError(w, "Invalid authentication cookie", http.StatusForbidden)
		return
	}

	var authCookie AuthCookie
	if err := json.Unmarshal(payload, &authCookie); err != nil {
		panic(err) // Programmer error
	}

	auth, err := authForUsername(r.Context(), authCookie.Name)
	if err != nil {
		authError(w, err.Error(), http.StatusForbidden)
		return
	}

	auth.AuthMethod = AUTH_COOKIE

	ctx := context.WithValue(r.Context(), userCtxKey, auth)
	r = r.WithContext(ctx)
	next.ServeHTTP(w, r)
}

type InternalAuth struct {
	// The username of the authenticated user
	Name string `json:"name"`

	// An arbitrary identifier for this internal user, e.g. "git.sr.ht"
	ClientID string `json:"client_id"`

	// An arbitrary identifier for this internal node, e.g. "us-east-3.git.sr.ht"
	NodeID string `json:"node_id"`

	// Only used by specific meta.sr.ht routes
	OAuthClientUUID string `json:"oauth_client_id,omit-empty"`
}

func internalAuth(internalNet []*net.IPNet, payload []byte,
	w http.ResponseWriter, r *http.Request, next http.Handler) {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	if ip == nil {
		panic(fmt.Errorf("Unable to parse remote address"))
	}
	var ok bool = false
	for _, ipnet := range internalNet {
		ok = ok || ipnet.Contains(ip)
		if ok {
			break
		}
	}
	if !ok {
		authError(w, fmt.Sprintf("Invalid source IP %s for internal auth", ip), http.StatusUnauthorized)
		return
	}

	payload = crypto.DecryptWithExpiration(payload, 30*time.Second)
	if payload == nil {
		authError(w, "Invalid Authorization header (encryption error)", http.StatusForbidden)
		return
	}

	var internalAuth InternalAuth
	if err := json.Unmarshal(payload, &internalAuth); err != nil {
		panic(err) // Programmer error
	}

	if internalAuth.ClientID == "" || internalAuth.NodeID == "" {
		authError(w, "Invalid Authorization header (missing Client ID or Node ID)", http.StatusForbidden)
	}

	var auth *AuthContext
	if internalAuth.OAuthClientUUID != "" {
		auth, err = authForOAuthClient(r.Context(), internalAuth.OAuthClientUUID)
		if err == nil {
			auth.AuthMethod = AUTH_INTERNAL
		}
	} else if internalAuth.Name != "" {
		auth, err = authForUsername(r.Context(), internalAuth.Name)
		if err == nil {
			auth.AuthMethod = AUTH_INTERNAL
		}
	} else {
		// Using anonymous internal auth. This is only used in one specific
		// situation: registering for a new account.
		auth = &AuthContext{}
		auth.AuthMethod = AUTH_ANON_INTERNAL
	}
	if err != nil {
		authError(w, err.Error(), http.StatusForbidden)
		return
	}

	auth.InternalAuth = internalAuth

	ctx := context.WithValue(r.Context(), userCtxKey, auth)
	r = r.WithContext(ctx)
	next.ServeHTTP(w, r)
}

func FetchMetaProfile(ctx context.Context, username string, user *AuthContext) error {
	if config.ServiceName(ctx) == "meta.sr.ht" {
		panic(errors.New("Cannot fetch profile from ourselves"))
	}

	type GraphQLProfile struct {
		ID               int     `json:"id"`
		Username         string  `json:"username"`
		Email            string  `json:"email"`
		URL              *string `json:"url"`
		Location         *string `json:"location"`
		Bio              *string `json:"bio"`
		UserType         string  `json:"userType"`
		SuspensionNotice string  `json:"suspensionNotice"`
	}

	type GraphQLResponse struct {
		Data struct {
			Me GraphQLProfile `json:"me"`
		} `json:"data"`
	}

	query := client.GraphQLQuery{
		Query: `
			query {
				me {
					id
					username
					email
					url
					location
					bio
					userType
				}
			}`,
	}

	var result GraphQLResponse
	if err := client.Execute(ctx, username,
		"meta.sr.ht", query, &result); err != nil {
		return err
	}

	profile := result.Data.Me
	return database.WithTx(ctx, nil, func(tx *sql.Tx) error {
		// TODO: Make the database representation consistent with this
		ut := strings.ToLower(profile.UserType)
		row := tx.QueryRowContext(ctx, `
			INSERT INTO "user" (
				created,
				updated,
				username,
				email,
				user_type,
				url,
				location,
				bio,
				suspension_notice
			)
			VALUES (
				NOW() at time zone 'utc',
				NOW() at time zone 'utc',
				$1, $2, $3, $4, $5, $6, $7
			)
			ON CONFLICT DO NOTHING
			RETURNING
				id,
				created,
				updated,
				username,
				email,
				user_type,
				url,
				location,
				bio,
				suspension_notice;`,
			&profile.Username, &profile.Email, &ut, &profile.URL,
			&profile.Location, &profile.Bio, &profile.SuspensionNotice)

		// TODO: Register webhooks
		if err := row.Scan(&user.UserID, &user.Created, &user.Updated,
			&user.Username, &user.Email, &user.UserType, &user.URL,
			&user.Location, &user.Bio, &user.SuspensionNotice); err != nil {
			if err == sql.ErrNoRows {
				panic(errors.New("Failed to upsert user record from meta.sr.ht"))
			}
			return err
		}
		return nil
	})
}

func LookupUser(ctx context.Context, username string, user *AuthContext) error {
	return database.WithTx(ctx, &sql.TxOptions{
		Isolation: 0,
		ReadOnly:  true,
	}, func(tx *sql.Tx) error {
		var (
			err  error
			rows *sql.Rows
		)
		query := database.
			Select(ctx, []string{
				`u.id`, `u.username`,
				`u.created`, `u.updated`,
				`u.email`,
				`u.user_type`,
				`u.url`, `u.location`, `u.bio`,
				`u.suspension_notice`,
			}).
			From(`"user" u`).
			Where(`u.username = ?`, username)
		if config.ServiceName(ctx) == "meta.sr.ht" {
			query = query.
				LeftJoin(`pgpkey p ON p.id = u.pgp_key_id`).
				Column(`p.key`)
		}
		if rows, err = query.RunWith(tx).Query(); err != nil {
			return err
		}
		defer rows.Close()

		if !rows.Next() {
			if err = rows.Err(); err != nil {
				return err
			}
			return FetchMetaProfile(ctx, username, user)
		}
		cols := []interface{}{
			&user.UserID, &user.Username,
			&user.Created, &user.Updated,
			&user.Email,
			&user.UserType,
			&user.URL,
			&user.Location,
			&user.Bio,
			&user.SuspensionNotice,
		}
		if config.ServiceName(ctx) == "meta.sr.ht" {
			cols = append(cols, &user.PGPKey)
		}
		if err = rows.Scan(cols...); err != nil {
			return err
		}
		if rows.Next() {
			if err = rows.Err(); err != nil {
				return err
			}
			panic(errors.New("Multiple users of the same username; invariant broken"))
		}
		return nil
	})
}

// Returns true if this token or client ID has been revoked (and therefore
// should not be trusted)
func LookupTokenRevocation(ctx context.Context,
	username string, hash [64]byte, clientID string) (bool, error) {
	type GraphQLResponse struct {
		Data struct {
			RevocationStatus bool `json:"tokenRevocationStatus"`
		} `json:"data"`
	}

	query := client.GraphQLQuery{
		Query: `
			query RevocationStatus($hash: String!, $clientId: String) {
				tokenRevocationStatus(hash: $hash, clientId: $clientId)
			}`,
		Variables: map[string]interface{}{
			"hash":     hex.EncodeToString(hash[:]),
			"clientId": clientID,
		},
	}

	var result GraphQLResponse
	if err := client.Execute(ctx, username,
		"meta.sr.ht", query, &result); err != nil {
		return true, err
	}
	return result.Data.RevocationStatus, nil
}

func OAuth2(token string, hash [64]byte, w http.ResponseWriter,
	r *http.Request, next http.Handler) {
	var (
		auth    AuthContext
		err     error
		res     int32
		tempErr int32
		wg      sync.WaitGroup
	)
	wg.Add(2)

	bt := DecodeBearerToken(token)
	if bt == nil {
		authError(w, `Invalid or expired OAuth 2.0 bearer token`, http.StatusForbidden)
		return
	}

	go func() {
		defer wg.Done()
		err = LookupUser(r.Context(), bt.Username, &auth)
		if err != nil {
			log.Printf("LookupUser: %v", err)
			atomic.AddInt32(&tempErr, 1)
		} else {
			atomic.AddInt32(&res, 1)
		}
	}()

	go func() {
		defer wg.Done()
		isRevoked, err := LookupTokenRevocation(r.Context(),
			bt.Username, hash, bt.ClientID)
		if err != nil {
			log.Printf("LookupTokenRevocation: %v", err)
			atomic.AddInt32(&tempErr, 1)
		} else if !isRevoked {
			atomic.AddInt32(&res, 1)
		}
	}()

	wg.Wait()
	if res != 2 {
		if tempErr != 0 {
			authError(w, "Temporary error; try again later", http.StatusInternalServerError)
		} else {
			authError(w, "Invalid or expired OAuth 2.0 bearer token", http.StatusForbidden)
		}
		return
	}

	if auth.UserType == USER_SUSPENDED {
		authError(w, fmt.Sprintf(
			"Account suspended with the following notice: %s\nContact support",
			*auth.SuspensionNotice), http.StatusForbidden)
		return
	}

	auth.AuthMethod = AUTH_OAUTH2
	auth.BearerToken = bt
	auth.TokenHash = hash
	auth.Grants = DecodeGrants(r.Context(), bt.Grants)

	ctx := context.WithValue(r.Context(), userCtxKey, &auth)
	r = r.WithContext(ctx)
	next.ServeHTTP(w, r)
}

// TODO: Remove legacy OAuth support
func LegacyOAuth(bearer string, hash [64]byte, w http.ResponseWriter,
	r *http.Request, next http.Handler) {
	var (
		auth    AuthContext
		expires time.Time
		scopes  string
	)
	if err := database.WithTx(r.Context(), &sql.TxOptions{
		Isolation: 0,
		ReadOnly:  true,
	}, func(tx *sql.Tx) error {
		var (
			err  error
			rows *sql.Rows
		)
		query := database.
			Select(r.Context(), []string{
				`ot.expires`,
				`ot.scopes`,
				`u.id`, `u.username`,
				`u.created`, `u.updated`,
				`u.email`,
				`u.user_type`,
				`u.url`, `u.location`, `u.bio`,
				`u.suspension_notice`,
			}).
			From(`oauthtoken ot`).
			Join(`"user" u ON u.id = ot.user_id`).
			Where(`ot.token_hash = ?`, bearer)
		if rows, err = query.RunWith(tx).Query(); err != nil {
			panic(err)
		}
		defer rows.Close()

		if !rows.Next() {
			if err := rows.Err(); err != nil {
				panic(err)
			}
			authError(w, "Invalid or expired OAuth token", http.StatusForbidden)
			return nil
		}
		if err := rows.Scan(&expires, &scopes,
			&auth.UserID, &auth.Username,
			&auth.Created, &auth.Updated,
			&auth.Email,
			&auth.UserType,
			&auth.URL,
			&auth.Location,
			&auth.Bio,
			&auth.SuspensionNotice); err != nil {
			panic(err)
		}
		if rows.Next() {
			if err := rows.Err(); err != nil {
				panic(err)
			}
			panic(errors.New("Multiple matching OAuth tokens; invariant broken"))
		}
		return nil
	}); err != nil {
		panic(err)
	}

	if time.Now().UTC().After(expires) {
		authError(w, "Invalid or expired OAuth token", http.StatusForbidden)
		return
	}

	if auth.UserType == USER_SUSPENDED {
		authError(w, fmt.Sprintf(
			"Account suspended with the following notice: %s\nContact support",
			*auth.SuspensionNotice), http.StatusForbidden)
		return
	}

	if scopes != "*" {
		authError(w, "Presently, OAuth authentication to the GraphQL API is only supported for OAuth tokens with all permissions, namely '*'.", http.StatusForbidden)
		return
	}

	auth.AuthMethod = AUTH_OAUTH_LEGACY

	ctx := context.WithValue(r.Context(), userCtxKey, &auth)
	r = r.WithContext(ctx)
	next.ServeHTTP(w, r)
}

// Returns an auth context configured for webhook delivery. This auth
// configuration is not possible during a normal GraphQL query, and is only
// used during webhook execution.
//
// The "ctx" parameter should be a webhook context, and the "auth" parameter
// should be the authentication context from the request which caused the
// webhook to be fired.
func WebhookAuth(ctx context.Context, auth *AuthContext,
	tokenHash [64]byte, grants string, clientID *string,
	expires time.Time) (context.Context, error) {
	if time.Now().UTC().After(expires) {
		return nil, fmt.Errorf("The authentication token used to create this webhook has expired")
	}

	whAuth := *auth
	whAuth.AuthMethod = AUTH_WEBHOOK
	whAuth.TokenHash = tokenHash
	whAuth.Grants = DecodeGrants(ctx, grants)
	whAuth.Grants.ReadOnly = true
	whAuth.BearerToken = &BearerToken{}
	if clientID != nil {
		whAuth.BearerToken.ClientID = *clientID
	}

	return context.WithValue(ctx, userCtxKey, &whAuth), nil
}

func Middleware(conf ini.File, apiconf string) func(http.Handler) http.Handler {
	var internalNet []*net.IPNet
	src, ok := conf.Get(apiconf, "internal-ipnet")
	if !ok {
		// Conservative default
		src = "127.0.0.1/24,::1/64"
	}
	for _, cidr := range strings.Split(src, ",") {
		_, ipnet, err := net.ParseCIDR(cidr)
		if err != nil {
			panic(err)
		}
		internalNet = append(internalNet, ipnet)
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !strings.HasPrefix(r.URL.Path, "/query") ||
				r.URL.Path == "/query/metrics" ||
				r.URL.Path == "/query/api-meta.json" {
				next.ServeHTTP(w, r)
				return
			}

			cookie, err := r.Cookie("sr.ht.unified-login.v1")
			if err == nil {
				cookieAuth(cookie, w, r, next)
				return
			}

			auth := r.Header.Get("Authorization")
			if auth == "" {
				authError(w, `Authorization header is required. Expected 'Authorization: Bearer [token]'`, http.StatusUnauthorized)
				return
			}

			z := strings.SplitN(auth, " ", 2)
			if len(z) != 2 {
				authError(w, "Invalid Authorization header", http.StatusBadRequest)
				return
			}

			var bearer string
			switch z[0] {
			case "Bearer":
				token := []byte(z[1])
				if oauth2BearerRegex.Match(token) {
					hash := sha512.Sum512(token)
					bearer = z[1]
					OAuth2(bearer, hash, w, r, next)
					return
				}
				if oauthBearerRegex.Match(token) {
					hash := sha512.Sum512(token)
					bearer = hex.EncodeToString(hash[:])
					LegacyOAuth(bearer, hash, w, r, next)
					return
				}
				authError(w, "Invalid OAuth bearer token", http.StatusBadRequest)
				return
			case "Internal":
				payload := []byte(z[1])
				internalAuth(internalNet, payload, w, r, next)
				return
			default:
				authError(w, "Invalid Authorization header", http.StatusBadRequest)
				return
			}
		})
	}
}

func ForContext(ctx context.Context) *AuthContext {
	raw, ok := ctx.Value(userCtxKey).(*AuthContext)
	if !ok {
		panic(errors.New("Invalid authentication context"))
	}
	return raw
}
