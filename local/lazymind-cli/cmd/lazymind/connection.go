package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"lazymind/agentconnector/internal/connectorconfig"
	"lazymind/agentconnector/internal/coreapi"
	"lazymind/agentconnector/internal/credentials"
	"lazymind/agentconnector/internal/workflowhost"
)

// Node host plugins consume the same Go resolver instead of maintaining a second
// implementation of address selection, account checks and legacy environment rules.
func resolveConnection(ctx context.Context, stdout io.Writer) error {
	c, err := connectorconfig.Current()
	if err != nil {
		return err
	}
	if c == nil || !c.Controlled || c.PairingFile == "" {
		return errors.New("a paired connection configuration is required; reconnect in LazyMind")
	}
	store, err := credentials.NewStore(c.Home, "")
	if err != nil {
		return err
	}
	scope, err := store.AccountScope()
	if err != nil {
		return err
	}
	body, err := os.ReadFile(c.PairingFile)
	if err != nil {
		return err
	}
	var pair workflowhost.Pairing
	if json.Unmarshal(body, &pair) != nil || pair.Provider != c.Provider || pair.Profile != c.Profile {
		return errors.New("connection pairing identity mismatch")
	}
	if _, err = workflowhost.Verify(c.Home, pair.ConnectorID, pair.Token, scope); err != nil {
		return err
	}
	api, err := coreapi.New(store)
	if err != nil {
		return err
	}
	web, err := api.WebURL(ctx)
	if err != nil {
		return err
	}
	bridge, err := connectorconfig.URL("LAZYMIND_ASSISTANT_BRIDGE_URL", connectorconfig.DefaultBridgeURL)
	if err != nil {
		return err
	}
	return printJSON(stdout, map[string]string{"provider": c.Provider, "webUrl": web, "bridgeUrl": bridge, "pairingFile": c.PairingFile, "serverName": "lazymind", "revision": fmt.Sprintf("%x", sha256.Sum256(body))})
}
