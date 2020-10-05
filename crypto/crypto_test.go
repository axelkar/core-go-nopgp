package crypto

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/vaughan0/go-ini"
)

func init() {
	config, err := ini.Load(strings.NewReader(`
[webhooks]
private-key=ebzsjPaN6E13ln/FeNWly1C92q6bVMVdOnDo1HPl5fc=

[sr.ht]
network-key=tbuG-7Vh44vrDq1L_HKWkHnWrDOtJhEkPKPiauaLeuk=`))
	if err != nil {
		panic(err)
	}
	InitCrypto(config)
}

func TestSignWebhook(t *testing.T) {
	payload := []byte("Hello world!")
	nonce, signature := SignWebhook(payload)

	sig, err := base64.StdEncoding.DecodeString(signature)
	assert.Nil(t, err)
	valid := Verify(append(payload, []byte(nonce)...), sig)
	assert.True(t, valid)
}

func TestSign(t *testing.T) {
	payload := []byte("Hello world!")
	signature := Sign(payload)

	valid := Verify(payload, signature)
	assert.True(t, valid)

	valid = Verify([]byte("Something else"), signature)
	assert.False(t, valid)
}

func TestEncrypt(t *testing.T) {
	payload := []byte("Hello, world!")

	enc := Encrypt(payload)
	assert.NotNil(t, enc)
	assert.NotEqual(t, enc, []byte("Hello, world!"))

	dec := Decrypt(enc)
	assert.NotNil(t, dec)
	assert.Equal(t, dec, []byte("Hello, world!"))
}

func TestEncryptWithExpire(t *testing.T) {
	payload := []byte("Hello, world!")

	enc := Encrypt(payload)
	assert.NotNil(t, enc)
	assert.NotEqual(t, enc, []byte("Hello, world!"))

	dec := DecryptWithExpiration(enc, 30*time.Minute)
	assert.NotNil(t, dec)
	assert.Equal(t, dec, []byte("Hello, world!"))

	time.Sleep(time.Duration(1))

	dec = DecryptWithExpiration(enc, time.Duration(2))
	assert.Nil(t, dec)
}

func TestHMAC(t *testing.T) {
	payload := []byte("Hello, world!")
	mac := HMAC(payload)

	valid := HMACVerify(payload, mac)
	assert.True(t, valid)

	valid = HMACVerify([]byte("Something else"), mac)
	assert.False(t, valid)
}
