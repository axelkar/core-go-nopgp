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
		log.Printf("Invalid bearer token: invalid base64 %e", err)
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
		log.Printf("Invalid bearer token: BARE unmarshal failed: %e", err)
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
