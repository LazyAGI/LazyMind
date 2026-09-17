package cloudclient

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCredentialRestoreClientListsOpaqueRecordReferencesWithoutStartingRestore(t *testing.T) {
	createCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/v1/credential-vault/restores" {
			createCalls++
		}
		if request.Method != http.MethodGet || request.URL.Path != "/v1/credential-vault/records" ||
			request.URL.Query().Get("page_size") != "20" || request.Header.Get("Authorization") != "Bearer fixture-access" {
			t.Errorf("record discovery request = %s %s?%s auth=%q", request.Method, request.URL.Path, request.URL.RawQuery, request.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"items":[{"record_id":"00000000-0000-7000-8000-000000000401","revision":7,"key_id":"credential-shard-17-v1","crypto_suite":"AES-256-GCM+RSA-3072-OAEP-SHA256+Ed25519","signing_member_id":"00000000-0000-7000-8000-000000000402","updated_at":"2026-08-26T10:00:00Z","etag_version":7,"etag":"`+strings.Repeat("e", 64)+`"}]}`)
	}))
	defer server.Close()
	client, err := New(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	page, err := client.ListCredentialVaultRecords(context.Background(), "fixture-access", "", 20)
	if err != nil {
		t.Fatalf("list credential restore references: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].Revision != 7 || createCalls != 0 {
		t.Fatalf("discovery page/create calls = %+v/%d", page, createCalls)
	}
}

func TestCredentialRestoreClientBindsIdempotencyRecipientAndNeverSendsPrivateKey(t *testing.T) {
	recipient, err := rsa.GenerateKey(rand.Reader, 3072)
	if err != nil {
		t.Fatal(err)
	}
	publicDER, err := x509.MarshalPKIXPublicKey(&recipient.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(publicDER)
	recordID := "00000000-0000-7000-8000-000000000401"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		if request.Method != http.MethodPost || request.URL.Path != "/v1/credential-vault/restores" ||
			request.Header.Get("Idempotency-Key") != "fixture-restore-idempotency" || request.Header.Get("Authorization") != "Bearer fixture-access" {
			t.Errorf("restore request = %s %s idempotency=%q", request.Method, request.URL.Path, request.Header.Get("Idempotency-Key"))
		}
		lower := bytes.ToLower(body)
		for _, forbidden := range [][]byte{[]byte("private_key"), []byte("private.pem"), []byte("api_key"), recipient.D.Bytes()} {
			if len(forbidden) > 0 && bytes.Contains(lower, bytes.ToLower(forbidden)) {
				t.Fatalf("restore request exposed private/plaintext material: %s", body)
			}
		}
		var requestBody CredentialRestoreRequest
		if err := json.Unmarshal(body, &requestBody); err != nil {
			t.Fatal(err)
		}
		if requestBody.Mode != "trusted_device" || !bytes.Equal(requestBody.RecipientPublicKey, publicDER) ||
			requestBody.RecipientPublicKeyHash != hex.EncodeToString(hash[:]) || len(requestBody.Records) != 1 {
			t.Fatalf("restore request body = %+v", requestBody)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, `{"operation_id":"00000000-0000-7000-8000-000000000403","status":"pending","expires_at":"2026-08-26T10:05:00Z","created_at":"2026-08-26T10:00:00Z"}`)
	}))
	defer server.Close()
	client, err := New(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	operation, err := client.CreateCredentialRestore(context.Background(), "fixture-access", "fixture-restore-idempotency", CredentialRestoreRequest{
		Mode: "trusted_device", Records: []CredentialRestoreRecordRequest{{RecordID: recordID, Revision: 7}},
		RecipientPublicKey: publicDER, RecipientPublicKeyHash: hex.EncodeToString(hash[:]),
	})
	if err != nil {
		t.Fatalf("create credential restore: %v", err)
	}
	if operation.Status != "pending" || !strings.HasSuffix(operation.OperationID, "0403") {
		t.Fatalf("restore operation = %+v", operation)
	}
}

func TestCredentialRestoreClientPollsAndCancelsOnlyTheExactOperation(t *testing.T) {
	operationID := "00000000-0000-7000-8000-000000000403"
	methods := make([]string, 0, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/credential-vault/restores/"+operationID || request.Header.Get("Authorization") != "Bearer fixture-access" {
			t.Errorf("operation request = %s %s", request.Method, request.URL.Path)
		}
		methods = append(methods, request.Method)
		if request.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"operation_id":"`+operationID+`","status":"running","expires_at":"2026-08-26T10:05:00Z","created_at":"2026-08-26T10:00:00Z"}`)
	}))
	defer server.Close()
	client, err := New(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.GetCredentialRestore(context.Background(), "fixture-access", operationID); err != nil {
		t.Fatalf("get credential restore: %v", err)
	}
	if err := client.CancelCredentialRestore(context.Background(), "fixture-access", operationID); err != nil {
		t.Fatalf("cancel credential restore: %v", err)
	}
	if strings.Join(methods, ",") != "GET,DELETE" {
		t.Fatalf("restore methods = %v", methods)
	}
}

func TestCredentialRestoreClientPreservesStableRecentAuthAndRateLimitErrors(t *testing.T) {
	recipient, err := rsa.GenerateKey(rand.Reader, 3072)
	if err != nil {
		t.Fatal(err)
	}
	publicDER, _ := x509.MarshalPKIXPublicKey(&recipient.PublicKey)
	digest := sha256.Sum256(publicDER)
	for name, fixture := range map[string]struct {
		status int
		code   int
		retry  string
	}{
		"recent auth": {http.StatusForbidden, 3080007, ""},
		"rate limit":  {http.StatusTooManyRequests, 3000006, "60"},
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if fixture.retry != "" {
					w.Header().Set("Retry-After", fixture.retry)
				}
				w.WriteHeader(fixture.status)
				_, _ = fmt.Fprintf(w, `{"code":%d,"message":"safe fixture","request_id":"fixture-request","retryable":false,"retry_after_seconds":null}`, fixture.code)
			}))
			defer server.Close()
			client, err := New(server.URL, server.Client())
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.CreateCredentialRestore(context.Background(), "fixture-access", "fixture-idempotency", CredentialRestoreRequest{
				Mode: "trusted_device", Records: []CredentialRestoreRecordRequest{{RecordID: "00000000-0000-7000-8000-000000000401", Revision: 7}},
				RecipientPublicKey: publicDER, RecipientPublicKeyHash: hex.EncodeToString(digest[:]),
			})
			var cloudErr *CloudError
			if !errors.As(err, &cloudErr) || cloudErr.HTTPStatus != fixture.status || cloudErr.Code != fixture.code || cloudErr.RequestID != "fixture-request" {
				t.Fatalf("stable restore error = %#v / %v", cloudErr, err)
			}
		})
	}
}
