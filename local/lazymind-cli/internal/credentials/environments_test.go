package credentials

import (
	"context"
	"net/http"
	"testing"

	"lazymind/agentconnector/internal/connectorconfig"
)

type unexpectedEnvironmentTransport struct{ t *testing.T }

func (transport unexpectedEnvironmentTransport) RoundTrip(*http.Request) (*http.Response, error) {
	transport.t.Fatal("Compose attempted to bootstrap an unrelated local Desktop session")
	return nil, nil
}

func TestComposeWithoutSessionNeverBootstrapsDesktop(t *testing.T) {
	root := t.TempDir()
	identity, _ := connectorconfig.OriginIdentity("http://localhost:8090")
	home, err := connectorconfig.EnvironmentHome(root, identity)
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(home, "")
	if err != nil {
		t.Fatal(err)
	}
	store.httpClient = &http.Client{Transport: unexpectedEnvironmentTransport{t}}
	if _, err := store.AccessToken(context.Background()); !IsAuthenticationRequired(err) {
		t.Fatalf("expected login requirement, got %v", err)
	}
	server, err := store.ServerURL(context.Background())
	if err != nil || server != "http://127.0.0.1:8090" {
		t.Fatalf("server=%s err=%v", server, err)
	}
	if err := store.Save(Credentials{ServerURL: "http://localhost:8091", AccessToken: "other", RefreshToken: "other"}); err == nil {
		t.Fatal("accepted another environment's credentials")
	}
}
