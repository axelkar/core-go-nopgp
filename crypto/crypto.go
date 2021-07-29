package crypto

import (
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"log"
	"time"

	"github.com/fernet/fernet-go"
	"github.com/vaughan0/go-ini"
)

var (
	webhookSk ed25519.PrivateKey
	webhookPk ed25519.PublicKey
	bearerKey []byte
	fernetKey *fernet.Key
)

func InitCrypto(config ini.File) {
	b64key, ok := config.Get("webhooks", "private-key")
	if !ok {
		log.Fatalf("No webhook key configured")
	}
	seed, err := base64.StdEncoding.DecodeString(b64key)
	if err != nil {
		log.Fatalf("base64 decode webhooks private key: %v", err)
	}
	webhookSk = ed25519.NewKeyFromSeed(seed)
	webhookPk, _ = webhookSk.Public().(ed25519.PublicKey)

	b64fernet, ok := config.Get("sr.ht", "network-key")
	if !ok {
		log.Fatalf("No network key configured")
	}
	fernetKey, err = fernet.DecodeKey(b64fernet)
	if err != nil {
		log.Fatalf("Load Fernet network encryption key: %v", err)
	}
	mac := hmac.New(sha256.New, webhookSk)
	mac.Write([]byte("sr.ht HMAC key"))
	bearerKey = mac.Sum(nil)
}

func Sign(payload []byte) []byte {
	return ed25519.Sign(webhookSk, payload)
}

func Verify(payload, signature []byte) bool {
	return ed25519.Verify(webhookPk, payload, signature)
}

func Encrypt(payload []byte) []byte {
	msg, err := fernet.EncryptAndSign(payload, fernetKey)
	if err != nil {
		log.Fatalf("Error encrypting payload: %v", err)
	}
	return msg
}

func DecryptWithoutExpiration(payload []byte) []byte {
	return fernet.VerifyAndDecrypt(payload,
		time.Duration(0), []*fernet.Key{fernetKey})
}

func DecryptWithExpiration(payload []byte, expiry time.Duration) []byte {
	if expiry == 0 {
		panic(fmt.Errorf("DecryptWithExpiration given expiration of zero. Use DecryptWithoutExpiration if you really meant it."))
	}
	return fernet.VerifyAndDecrypt(payload, expiry, []*fernet.Key{fernetKey})
}

func BearerHMAC(payload []byte) []byte {
	mac := hmac.New(sha256.New, bearerKey)
	mac.Write(payload)
	return mac.Sum(nil)
}

func BearerVerify(payload []byte, signature []byte) bool {
	mac := hmac.New(sha256.New, bearerKey)
	mac.Write(payload)
	expected := mac.Sum(nil)
	return hmac.Equal(expected, signature)
}

// Signs the payload for a webhook, returning respectively the values for the
// X-Payload-Nonce and X-Payload-Signature headers.
func SignWebhook(payload []byte) (string, string) {
	var nonceSeed [8]byte
	_, err := rand.Read(nonceSeed[:])
	if err != nil {
		panic(fmt.Errorf("Failed to generate nonce: %w", err))
	}

	nonce := hex.EncodeToString(nonceSeed[:])
	signature := base64.StdEncoding.EncodeToString(
		Sign(append(payload, []byte(nonce)...)))
	return nonce, signature
}

func VerifyWebhook(payload []byte, nonce, signature string) bool {
	s, err := base64.StdEncoding.DecodeString(signature)
	if err != nil {
		return false
	}
	return Verify(append(payload, []byte(nonce)...), s)
}
