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
	bt := &BearerToken{
		Version:  TokenVersion,
		Expires:  ToTimestamp(time.Now().Add(30 * time.Minute)),
		Grants:   "",
		ClientID: "",
		Username: "jdoe",
	}
	token := bt.Encode()
	bytes, err := base64.RawStdEncoding.DecodeString(token)
	assert.Nil(t, err)

	mac := bytes[len(bytes)-32:]
	payload := bytes[:len(bytes)-32]
	assert.True(t, crypto.BearerVerify(payload, mac))

	var bt2 BearerToken
	err = bare.Unmarshal(payload, &bt2)
	assert.Nil(t, err)
	assert.Equal(t, bt.Version, bt2.Version)
	assert.Equal(t, bt.Expires, bt2.Expires)
	assert.Equal(t, bt.Grants, bt2.Grants)
	assert.Equal(t, bt.ClientID, bt2.ClientID)
	assert.Equal(t, bt.Username, bt2.Username)
}

func TestDecode(t *testing.T) {
	bt := &BearerToken{
		Version:  TokenVersion,
		Expires:  ToTimestamp(time.Now().Add(30 * time.Minute)),
		Grants:   "",
		ClientID: "",
		Username: "jdoe",
	}
	token := bt.Encode()
	bt2 := DecodeBearerToken(token)
	assert.NotNil(t, bt2)
	assert.Equal(t, bt.Version, bt2.Version)
	assert.Equal(t, bt.Expires, bt2.Expires)
	assert.Equal(t, bt.Grants, bt2.Grants)
	assert.Equal(t, bt.ClientID, bt2.ClientID)
	assert.Equal(t, bt.Username, bt2.Username)

	// Expired token:
	bt = &BearerToken{
		Version:  TokenVersion,
		Expires:  ToTimestamp(time.Now().Add(-30 * time.Minute)),
		Grants:   "",
		ClientID: "",
		Username: "jdoe",
	}
	token = bt.Encode()
	bt2 = DecodeBearerToken(token)
	assert.Nil(t, bt2)

	// Invalid MAC:
	bt = &BearerToken{
		Version:  TokenVersion,
		Expires:  ToTimestamp(time.Now().Add(30 * time.Minute)),
		Grants:   "",
		ClientID: "",
		Username: "jdoe",
	}
	plain, err := bare.Marshal(bt)
	assert.Nil(t, err)
	mac := crypto.BearerHMAC(plain)
	bt.Username = "rdoe"
	plain, err = bare.Marshal(bt)
	assert.Nil(t, err)
	token = base64.RawStdEncoding.EncodeToString(append(plain, mac...))
	bt2 = DecodeBearerToken(token)
	assert.Nil(t, bt2)
}
