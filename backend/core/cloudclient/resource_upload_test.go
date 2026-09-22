package cloudclient

import (
	"context"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"lazymind/core/cloudpackage"
)

func uploadManifestFixture() cloudpackage.Manifest {
	return cloudpackage.Manifest{
		FormatSchema: "lazymind.resource-manifest/v2", ResourceType: "skill", ResourceName: "Fixture Skill",
		ClientResourceKey: "skill:local-a", Entrypoint: "SKILL.md",
		Files:       []cloudpackage.ManifestFile{{Path: "SKILL.md", Size: 7, SHA256: strings.Repeat("a", 64)}},
		ContentSize: 7, ContentHash: strings.Repeat("b", 64), TransportFormat: "zip",
		TransportSize: 7, TransportHash: strings.Repeat("c", 64), MinDesktopVersion: "1.0.0",
		RequiredCapabilities: []string{}, Permissions: []string{},
	}
}

func TestBeginResourceUpsertUsesBearerIdempotencyAndCreateCondition(t *testing.T) {
	httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodPost || request.URL.Path != "/v1/resource-upserts" {
			t.Fatalf("request = %s %s", request.Method, request.URL.Path)
		}
		if request.Header.Get("Authorization") != "Bearer fixture-access" || request.Header.Get("Idempotency-Key") != "fixture-upsert-key" || request.Header.Get("If-None-Match") != "*" {
			t.Fatalf("upsert headers = %v", request.Header)
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		for _, required := range []string{`"resource_type":"skill"`, `"client_resource_key":"skill:local-a"`, `"transport_hash"`} {
			if !strings.Contains(string(body), required) {
				t.Fatalf("upsert body omitted %s: %s", required, body)
			}
		}
		return jsonResponse(http.StatusCreated, `{
			"operation_id":"00000000-0000-7000-8000-000000000101",
			"status":"waiting_for_upload",
			"upload":{"upload_id":"00000000-0000-7000-8000-000000000102","mode":"single_put","url":"https://objects.example/upload","method":"PUT","headers":{"Content-Type":"application/zip"},"expires_at":"2030-08-24T10:00:00Z"}
		}`, nil), nil
	})}
	client, err := New("https://cloud.example", httpClient)
	if err != nil {
		t.Fatal(err)
	}
	operation, err := client.BeginResourceUpsert(context.Background(), "fixture-access", ResourceUpsertRequest{
		ResourceType: "skill", ClientResourceKey: "skill:local-a", Manifest: uploadManifestFixture(),
		IdempotencyKey: "fixture-upsert-key", IfNoneMatch: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if operation.Status != ResourceOperationWaitingForUpload || operation.Upload == nil || operation.Upload.Mode != UploadModeSinglePut {
		t.Fatalf("operation = %+v", operation)
	}
}

func TestResourceUpsertUpdateUsesQuotedCurrentCloudHash(t *testing.T) {
	hash := strings.Repeat("d", 64)
	httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Header.Get("If-Match") != `"`+hash+`"` || request.Header.Get("If-None-Match") != "" {
			t.Fatalf("conditional headers = %v", request.Header)
		}
		return jsonResponse(http.StatusOK, `{
			"operation_id":"00000000-0000-7000-8000-000000000103","status":"succeeded",
			"resource":{"resource_id":"00000000-0000-7000-8000-000000000104","resource_type":"skill","client_resource_key":"skill:local-a","resource_name":"Fixture Skill","content_hash":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","content_size":7,"format_schema":"lazymind.resource-manifest/v2","updated_at":"2026-08-24T10:00:00Z"}
		}`, nil), nil
	})}
	client, _ := New("https://cloud.example", httpClient)
	operation, err := client.BeginResourceUpsert(context.Background(), "fixture-access", ResourceUpsertRequest{
		ResourceType: "skill", ClientResourceKey: "skill:local-a", Manifest: uploadManifestFixture(),
		IdempotencyKey: "fixture-upsert-update", IfMatch: hash,
	})
	if err != nil || operation.Status != ResourceOperationSucceeded || operation.Resource == nil {
		t.Fatalf("operation=%+v err=%v", operation, err)
	}
}

func TestSignedUploadDoesNotForwardCloudCredentialsAndRejectsRedirect(t *testing.T) {
	temporary, err := os.CreateTemp(t.TempDir(), "skill-upload-*.zip")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := temporary.WriteString("fixture-upload-body"); err != nil {
		t.Fatal(err)
	}
	if err := temporary.Close(); err != nil {
		t.Fatal(err)
	}

	t.Run("bounded PUT", func(t *testing.T) {
		httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			if request.Method != http.MethodPut || request.Header.Get("Authorization") != "" || request.Header.Get("Cookie") != "" {
				t.Fatalf("signed upload leaked Cloud credentials: method=%s headers=%v", request.Method, request.Header)
			}
			body, _ := io.ReadAll(request.Body)
			if string(body) != "fixture-upload-body" || request.Header.Get("Content-Type") != "application/zip" {
				t.Fatalf("signed upload body=%q headers=%v", body, request.Header)
			}
			return jsonResponse(http.StatusOK, "", map[string]string{"ETag": `"fixture-etag"`}), nil
		})}
		client, _ := New("https://cloud.example", httpClient)
		etag, err := client.PutSignedFile(context.Background(), SignedRequest{
			URL: "https://objects.example/upload", Method: http.MethodPut,
			Headers: map[string]string{"Content-Type": "application/zip"}, ExpiresAt: time.Now().Add(time.Minute).Format(time.RFC3339),
		}, temporary.Name(), 0, int64(len("fixture-upload-body")))
		if err != nil || etag != `"fixture-etag"` {
			t.Fatalf("etag=%q err=%v", etag, err)
		}
	})

	t.Run("redirect rejected", func(t *testing.T) {
		httpClient := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusTemporaryRedirect, "", map[string]string{"Location": "https://attacker.example/upload"}), nil
		})}
		client, _ := New("https://cloud.example", httpClient)
		if _, err := client.PutSignedFile(context.Background(), SignedRequest{
			URL: "https://objects.example/upload", Method: http.MethodPut, ExpiresAt: time.Now().Add(time.Minute).Format(time.RFC3339),
		}, temporary.Name(), 0, int64(len("fixture-upload-body"))); err == nil {
			t.Fatal("signed upload followed or accepted a redirect")
		}
	})
}

func TestMultipartPresignAndCompletionKeepPartNumbersStable(t *testing.T) {
	call := 0
	httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		call++
		switch call {
		case 1:
			if request.URL.Path != "/v1/transfers/uploads/00000000-0000-7000-8000-000000000105/parts:presign" {
				t.Fatalf("presign path = %s", request.URL.Path)
			}
			return jsonResponse(http.StatusOK, `{"items":[
				{"part_number":1,"url":"https://objects.example/part-1","method":"PUT","headers":{},"expires_at":"2030-08-24T10:00:00Z"},
				{"part_number":2,"url":"https://objects.example/part-2","method":"PUT","headers":{},"expires_at":"2030-08-24T10:00:00Z"}
			]}`, nil), nil
		case 2:
			if request.URL.Path != "/v1/transfers/uploads/00000000-0000-7000-8000-000000000105:complete" {
				t.Fatalf("complete path = %s", request.URL.Path)
			}
			body, _ := io.ReadAll(request.Body)
			if !strings.Contains(string(body), `"part_number":1`) || !strings.Contains(string(body), `"etag":"etag-2"`) {
				t.Fatalf("complete body = %s", body)
			}
			return jsonResponse(http.StatusAccepted, `{"upload_id":"00000000-0000-7000-8000-000000000105","operation_id":"00000000-0000-7000-8000-000000000106","mode":"multipart","status":"verifying","expires_at":"2030-08-24T10:00:00Z"}`, nil), nil
		default:
			t.Fatalf("unexpected call %d", call)
			return nil, nil
		}
	})}
	client, _ := New("https://cloud.example", httpClient)
	parts, err := client.PresignUploadParts(context.Background(), "fixture-access", "00000000-0000-7000-8000-000000000105", []int{1, 2})
	if err != nil || len(parts) != 2 || parts[0].PartNumber != 1 || parts[1].PartNumber != 2 {
		t.Fatalf("parts=%+v err=%v", parts, err)
	}
	if err := client.CompleteResourceUpload(context.Background(), "fixture-access", "00000000-0000-7000-8000-000000000105", []CompletedUploadPart{{PartNumber: 1, ETag: "etag-1"}, {PartNumber: 2, ETag: "etag-2"}}); err != nil {
		t.Fatal(err)
	}
}
