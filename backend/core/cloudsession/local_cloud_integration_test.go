//go:build integration

package cloudsession

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"testing"
	"time"

	"lazymind/core/cloudclient"
)

func TestLocalCloudTokenPairCanEstablishSecureSession(t *testing.T) {
	baseURL := os.Getenv("LAZYMIND_TEST_CLOUD_BASE_URL")
	username := os.Getenv("LAZYMIND_TEST_CLOUD_USERNAME")
	password := os.Getenv("LAZYMIND_TEST_CLOUD_PASSWORD")
	if baseURL == "" || username == "" || password == "" {
		t.Fatal("local Cloud integration prerequisites are not configured")
	}
	body, err := json.Marshal(map[string]string{
		"login": username, "password": password, "client_type": "desktop", "client_label": "LazyMind integration test",
	})
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodPost, baseURL+"/v1/auth/login", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("local Cloud login status = %d", response.StatusCode)
	}
	var tokens cloudclient.AuthTokens
	if err := json.NewDecoder(response.Body).Decode(&tokens); err != nil {
		t.Fatal(err)
	}
	expiresAt, err := cloudclient.AccessTokenExpiresAt(tokens.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	client, err := cloudclient.New(baseURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	store := NewSystemSecureTokenStore(baseURL + "#integration-test")
	service := NewService(ServiceDeps{Store: store, Auth: CloudAuthClient{Client: client}})
	_ = store.Delete(context.Background())
	t.Cleanup(func() {
		logoutContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = service.Logout(logoutContext)
	})
	if err := service.Establish(context.Background(), TokenPair{
		AccessToken: tokens.AccessToken, RefreshToken: tokens.RefreshToken, AccessExpiresAt: expiresAt,
	}); err != nil {
		t.Fatalf("establish local Cloud session: %v", err)
	}
	if service.Status(context.Background()).State != StateSignedIn {
		t.Fatal("local Cloud session did not become signed_in")
	}
}
