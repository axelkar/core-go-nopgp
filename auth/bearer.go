package auth

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"log"
	"strings"
	"time"

	"git.sr.ht/~sircmpwn/go-bare"

	"git.sr.ht/~sircmpwn/core-go/config"
	"git.sr.ht/~sircmpwn/core-go/crypto"
)

const TokenVersion uint = 0

type Timestamp int64

func (t Timestamp) Time() time.Time {
	return time.Unix(int64(t), 0).UTC()
}

func ToTimestamp(t time.Time) Timestamp {
	return Timestamp(t.UTC().Unix())
}

type BearerToken struct {
	Version  uint
	Expires  Timestamp
	Grants   string
	ClientID string
	Username string
}

func (bt *BearerToken) Encode() string {
	plain, err := bare.Marshal(bt)
	if err != nil {
		panic(err)
	}
	mac := crypto.BearerHMAC(plain)
	return base64.RawStdEncoding.EncodeToString(append(plain, mac...))
}

func DecodeBearerToken(token string) *BearerToken {
	payload, err := base64.RawStdEncoding.DecodeString(token)
	if err != nil {
		log.Printf("Invalid bearer token: invalid base64: %v", err)
		return nil
	}
	if len(payload) <= 32 {
		log.Printf("Invalid bearer token: payload <32 bytes")
		return nil
	}

	mac := payload[len(payload)-32:]
	payload = payload[:len(payload)-32]
	if crypto.BearerVerify(payload, mac) == false {
		log.Printf("Invalid bearer token: HMAC verification failed (MAC: [%d]%s; payload: [%d]%s",
			len(mac), hex.EncodeToString(mac), len(payload), hex.EncodeToString(payload))
		return nil
	}

	var bt BearerToken
	err = bare.Unmarshal(payload, &bt)
	if err != nil {
		log.Printf("Invalid bearer token: BARE unmarshal failed: %v", err)
		return nil
	}
	if bt.Version != TokenVersion {
		log.Printf("Invalid bearer token: invalid token version")
		return nil
	}
	if time.Now().UTC().After(bt.Expires.Time()) {
		log.Printf("Invalid bearer token: token expired")
		return nil
	}
	return &bt
}

const (
	RO = "RO"
	RW = "RW"
)

type Grants struct {
	ReadOnly bool

	all      bool
	grants   map[string]string
	encoded  string
}

func DecodeGrants(ctx context.Context, grants string) Grants {
	if grants == "" {
		// All permissions
		return Grants{
			all:     true,
			grants:  nil,
			encoded: "",
		}
	}
	accessMap := make(map[string]string)
	for _, grant := range strings.Split(grants, " ") {
		var (
			service string
			scope   string
			access  string
		)
		parts := strings.Split(grant, "/")
		if len(parts) != 2 {
			panic(fmt.Errorf("OAuth grant '%s' without service/scope format", grant))
		}
		service = parts[0]
		parts = strings.Split(parts[1], ":")
		scope = parts[0]
		if len(parts) == 1 {
			access = "RO"
		} else {
			access = parts[1]
		}
		if service == config.ServiceName(ctx) {
			accessMap[scope] = access
		}
	}
	return Grants{
		all:     false,
		grants:  accessMap,
		encoded: grants,
	}
}

func (g *Grants) Has(grant string, mode string) bool {
	if mode != RO && mode != RW {
		panic("Invalid access mode")
	}
	if g.ReadOnly && mode == RW {
		return false
	}

	if g.all {
		return true
	}

	if access, ok := g.grants[grant]; !ok {
		return false
	} else {
		if mode == RO {
			return true
		}
		return mode == access
	}
}

func (g *Grants) Encode() string {
	return g.encoded
}
