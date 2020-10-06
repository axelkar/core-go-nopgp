package auth

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"git.sr.ht/~sircmpwn/go-bare"
	"github.com/stretchr/testify/assert"
	"github.com/vaughan0/go-ini"

	"git.sr.ht/~sircmpwn/core-go/crypto"
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
	crypto.InitCrypto(config)
}

func TestEncode(t *testing.T) {
	ot := &OAuth2Token{
		Version:  TokenVersion,
		Expires:  ToTimestamp(time.Now().Add(30 * time.Minute)),
		Grants:   "",
		ClientID: "",
		Username: "jdoe",
	}
	token := ot.Encode()
	bytes, err := base64.RawStdEncoding.DecodeString(token)
	assert.Nil(t, err)

	mac := bytes[len(bytes)-32:]
	payload := bytes[:len(bytes)-32]
	assert.True(t, crypto.HMACVerify(payload, mac))

	var ot2 OAuth2Token
	err = bare.Unmarshal(payload, &ot2)
	assert.Nil(t, err)
	assert.Equal(t, ot.Version, ot2.Version)
	assert.Equal(t, ot.Expires, ot2.Expires)
	assert.Equal(t, ot.Grants, ot2.Grants)
	assert.Equal(t, ot.ClientID, ot2.ClientID)
	assert.Equal(t, ot.Username, ot2.Username)
}

func TestDecode(t *testing.T) {
	ot := &OAuth2Token{
		Version:  TokenVersion,
		Expires:  ToTimestamp(time.Now().Add(30 * time.Minute)),
		Grants:   "",
		ClientID: "",
		Username: "jdoe",
	}
	token := ot.Encode()
	ot2 := DecodeToken(token)
	assert.NotNil(t, ot2)
	assert.Equal(t, ot.Version, ot2.Version)
	assert.Equal(t, ot.Expires, ot2.Expires)
	assert.Equal(t, ot.Grants, ot2.Grants)
	assert.Equal(t, ot.ClientID, ot2.ClientID)
	assert.Equal(t, ot.Username, ot2.Username)

	// Expired token:
	ot = &OAuth2Token{
		Version:  TokenVersion,
		Expires:  ToTimestamp(time.Now().Add(-30 * time.Minute)),
		Grants:   "",
		ClientID: "",
		Username: "jdoe",
	}
	token = ot.Encode()
	ot2 = DecodeToken(token)
	assert.Nil(t, ot2)

	// Invalid MAC:
	ot = &OAuth2Token{
		Version:  TokenVersion,
		Expires:  ToTimestamp(time.Now().Add(30 * time.Minute)),
		Grants:   "",
		ClientID: "",
		Username: "jdoe",
	}
	plain, err := bare.Marshal(ot)
	assert.Nil(t, err)
	mac := crypto.HMAC(plain)
	ot.Username = "rdoe"
	plain, err = bare.Marshal(ot)
	assert.Nil(t, err)
	token = base64.RawStdEncoding.EncodeToString(append(plain, mac...))
	ot2 = DecodeToken(token)
	assert.Nil(t, ot2)
}
