//go:build integration

package cloudsession

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
)

func TestRunningLocalCoreCompletesCloudBrowserHandoff(t *testing.T) {
	coreURL := os.Getenv("LAZYMIND_TEST_CORE_URL")
	cloudURL := os.Getenv("LAZYMIND_TEST_CLOUD_BASE_URL")
	username := os.Getenv("LAZYMIND_TEST_CLOUD_USERNAME")
	password := os.Getenv("LAZYMIND_TEST_CLOUD_PASSWORD")
	if coreURL == "" || cloudURL == "" || username == "" || password == "" {
		t.Fatal("running local Handoff prerequisites are not configured")
	}

	loginStartResponse, err := http.Post(coreURL+"/cloud/login", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer loginStartResponse.Body.Close()
	if loginStartResponse.StatusCode != http.StatusOK {
		t.Fatalf("Core login start status = %d", loginStartResponse.StatusCode)
	}
	var loginStart struct {
		Data struct {
			AuthorizationURL string `json:"authorization_url"`
		} `json:"data"`
	}
	if err := json.NewDecoder(loginStartResponse.Body).Decode(&loginStart); err != nil {
		t.Fatal(err)
	}
	authorizationURL, err := url.Parse(loginStart.Data.AuthorizationURL)
	if err != nil || authorizationURL.Path != "/zh/desktop/authorize" {
		t.Fatal("Core returned an invalid authorization URL")
	}

	browserLoginBody, _ := json.Marshal(map[string]string{"login": username, "password": password})
	browserLoginRequest, _ := http.NewRequest(http.MethodPost, cloudURL+"/v1/browser/auth/login", bytes.NewReader(browserLoginBody))
	browserLoginRequest.Header.Set("Content-Type", "application/json")
	browserLoginRequest.Header.Set("Origin", cloudURL)
	browserLoginResponse, err := http.DefaultClient.Do(browserLoginRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer browserLoginResponse.Body.Close()
	if browserLoginResponse.StatusCode != http.StatusOK {
		t.Fatalf("Browser login status = %d", browserLoginResponse.StatusCode)
	}
	var browserTokens struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(browserLoginResponse.Body).Decode(&browserTokens); err != nil {
		t.Fatal(err)
	}
	cookies := map[string]string{}
	for _, cookie := range browserLoginResponse.Cookies() {
		cookies[cookie.Name] = cookie.Value
	}
	refresh, csrf := cookies["__Host-lazymind-refresh"], cookies["__Host-lazymind-csrf"]
	if browserTokens.AccessToken == "" || refresh == "" || csrf == "" {
		t.Fatal("Browser login omitted required in-memory credentials")
	}

	query := authorizationURL.Query()
	transactionBody, _ := json.Marshal(map[string]string{
		"client_id": query.Get("client_id"), "redirect_uri": query.Get("redirect_uri"),
		"state": query.Get("state"), "code_challenge": query.Get("code_challenge"),
		"code_challenge_method": query.Get("code_challenge_method"),
	})
	transactionRequest, _ := http.NewRequest(http.MethodPost, cloudURL+"/v1/desktop-auth/transactions", bytes.NewReader(transactionBody))
	transactionRequest.Header.Set("Content-Type", "application/json")
	transactionRequest.Header.Set("Origin", cloudURL)
	transactionRequest.Header.Set("Authorization", "Bearer "+browserTokens.AccessToken)
	transactionRequest.Header.Set("X-CSRF-Token", csrf)
	transactionRequest.Header.Set("Cookie", "__Host-lazymind-refresh="+refresh+"; __Host-lazymind-csrf="+csrf)
	transactionResponse, err := http.DefaultClient.Do(transactionRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer transactionResponse.Body.Close()
	if transactionResponse.StatusCode != http.StatusCreated {
		t.Fatalf("Desktop transaction status = %d", transactionResponse.StatusCode)
	}
	var transaction struct {
		RedirectTo string `json:"redirect_to"`
	}
	if err := json.NewDecoder(transactionResponse.Body).Decode(&transaction); err != nil {
		t.Fatal(err)
	}
	callbackURL, err := url.Parse(transaction.RedirectTo)
	if err != nil || callbackURL.Hostname() != "127.0.0.1" || callbackURL.Path != "/cloud-auth/callback" ||
		callbackURL.Query().Get("code") == "" || callbackURL.Query().Get("state") != query.Get("state") {
		t.Fatal("Cloud returned an invalid Loopback result")
	}
	callbackResponse, err := http.Get(callbackURL.String())
	if err != nil {
		t.Fatal(err)
	}
	defer callbackResponse.Body.Close()
	if callbackResponse.StatusCode != http.StatusOK {
		t.Fatalf("Loopback callback status = %d", callbackResponse.StatusCode)
	}

	sessionResponse, err := http.Get(coreURL + "/cloud/session")
	if err != nil {
		t.Fatal(err)
	}
	defer sessionResponse.Body.Close()
	var session struct {
		Data Status `json:"data"`
	}
	if err := json.NewDecoder(sessionResponse.Body).Decode(&session); err != nil {
		t.Fatal(err)
	}
	if session.Data.State != StateSignedIn || session.Data.Username != username || strings.TrimSpace(session.Data.RegistrationURL) == "" {
		t.Fatalf("Core session state = %q username_match=%v registration_url=%v", session.Data.State, session.Data.Username == username, session.Data.RegistrationURL != "")
	}
}
