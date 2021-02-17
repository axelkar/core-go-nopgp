package auth

import (
	"encoding/base64"
	"encoding/hex"
	"log"
	"time"

	"git.sr.ht/~sircmpwn/go-bare"

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

type OAuth2Token struct {
	Version  uint
	Expires  Timestamp
	Grants   string
	ClientID string
	Username string
}

func (ot *OAuth2Token) Encode() string {
	plain, err := bare.Marshal(ot)
	if err != nil {
		panic(err)
	}
	mac := crypto.HMAC(plain)
	return base64.RawStdEncoding.EncodeToString(append(plain, mac...))
}

func DecodeToken(token string) *OAuth2Token {
	payload, err := base64.RawStdEncoding.DecodeString(token)
	if err != nil {
		log.Printf("Invalid bearer token: invalid base64 %e", err)
		return nil
	}
	if len(payload) <= 32 {
		log.Printf("Invalid bearer token: payload <32 bytes")
		return nil
	}

	mac := payload[len(payload)-32:]
	payload = payload[:len(payload)-32]
	if crypto.HMACVerify(payload, mac) == false {
		log.Printf("Invalid bearer token: HMAC verification failed (MAC: [%d]%s; payload: [%d]%s",
			len(mac), hex.EncodeToString(mac), len(payload), hex.EncodeToString(payload))
		return nil
	}

	var ot OAuth2Token
	err = bare.Unmarshal(payload, &ot)
	if err != nil {
		log.Printf("Invalid bearer token: BARE unmarshal failed: %e", err)
		return nil
	}
	if ot.Version != TokenVersion {
		log.Printf("Invalid bearer token: invalid token version")
		return nil
	}
	if time.Now().UTC().After(ot.Expires.Time()) {
		log.Printf("Invalid bearer token: token expired")
		return nil
	}
	return &ot
}
