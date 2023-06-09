package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"

	"git.sr.ht/~sircmpwn/core-go/config"
	"git.sr.ht/~sircmpwn/core-go/crypto"
)

type GraphQLQuery struct {
	Query     string                 `json:"query"`
	Variables map[string]interface{} `json:"variables"`
}

type InternalAuth struct {
	Name     string `json:"name"`
	ClientID string `json:"client_id"`
	NodeID   string `json:"node_id"`
}

func Execute(ctx context.Context, username string, svc string,
	query GraphQLQuery, result interface{}) error {
	body, err := json.Marshal(query)
	if err != nil {
		panic(err) // Programmer error
	}

	conf := config.ForContext(ctx)
	origin, _ := conf.Get(svc, "api-origin")
	if origin == "" {
		origin = config.GetOrigin(conf, svc, false)
	}
	if origin == "" {
		panic(fmt.Errorf("No %s origin specified in config.ini", svc))
	}

	reader := bytes.NewBuffer(body)
	req, err := http.NewRequestWithContext(ctx,
		"POST", fmt.Sprintf("%s/query", origin), reader)
	if err != nil {
		return err
	}
	req.Header.Add("Content-Type", "application/json")
	auth := InternalAuth{
		Name:     username,
		ClientID: config.ServiceName(ctx),
		// TODO: Populate this:
		NodeID: "core-go",
	}
	authBlob, err := json.Marshal(&auth)
	if err != nil {
		panic(err) // Programmer error
	}
	req.Header.Add("Authorization", fmt.Sprintf("Internal %s",
		crypto.Encrypt(authBlob)))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}

	defer resp.Body.Close()
	respBody, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	if resp.StatusCode != 200 {
		return fmt.Errorf("%s returned status %d: %s",
			svc, resp.StatusCode, string(respBody))
	}

	if err = json.Unmarshal(respBody, result); err != nil {
		return err
	}

	return nil
}
