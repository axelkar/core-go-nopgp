package crypto

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"log"

	"github.com/vaughan0/go-ini"
	"golang.org/x/crypto/ed25519"
)

var (
	webhookKey ed25519.PrivateKey
)

func initWebhookKey(logger *log.Logger, config ini.File) {
	if webhookKey != nil {
		return
	}

	b64key, ok := config.Get("webhooks", "private-key")
	if !ok {
		logger.Fatalf("No webhook key configured")
	}
	seed, err := base64.StdEncoding.DecodeString(b64key)
	if err != nil {
		logger.Fatalf("base64 decode webhooks private key: %v", err)
	}
	webhookKey = ed25519.NewKeyFromSeed(seed)
}

func SignWebhookPayload(payload []byte, logger *log.Logger, config ini.File) (string, string) {
	var (
		nonceSeed [8]byte
		nonceHex  [16]byte
	)

	_, err := rand.Read(nonceSeed[:])
	if err != nil {
		logger.Fatalf("generate nonce: %v", err)
	}
	hex.Encode(nonceHex[:], nonceSeed[:])
	nonce := string(nonceHex[:])

	initWebhookKey(logger, config)
	signature := base64.StdEncoding.EncodeToString(
		ed25519.Sign(webhookKey, append(payload, nonceHex[:]...)))

	return nonce, signature
}
