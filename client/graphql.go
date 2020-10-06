package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"

	"git.sr.ht/~sircmpwn/gql.sr.ht/config"
	"git.sr.ht/~sircmpwn/gql.sr.ht/crypto"
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
	origin, ok := conf.Get(svc, "origin")
	if !ok {
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
		Name: username,
		// TODO: Populate these better
		ClientID: "gql.sr.ht",
		NodeID:   "gql.sr.ht",
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
