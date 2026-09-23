package cloudclient

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func jsonResponse(status int, body string, headers map[string]string) *http.Response {
	response := &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
	for key, value := range headers {
		response.Header.Set(key, value)
	}
	return response
}

func TestListResourcesUsesOwnedResourceContract(t *testing.T) {
	httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodGet || request.URL.Path != "/v1/resources" {
			t.Fatalf("request = %s %s", request.Method, request.URL.Path)
		}
		if request.Header.Get("Authorization") != "Bearer fixture-access" {
			t.Fatalf("authorization = %q", request.Header.Get("Authorization"))
		}
		if got := request.URL.Query(); got.Get("resource_type") != "skill" || got.Get("cursor") != "next" || got.Get("page_size") != "20" {
			t.Fatalf("query = %v", got)
		}
		return jsonResponse(http.StatusOK, `{"items":[{"resource_id":"resource-1","resource_type":"skill","client_resource_key":"skill:a","resource_name":"A","content_hash":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","content_size":10,"format_schema":"lazymind.resource-manifest/v2","updated_at":"2026-08-20T10:00:00+08:00"}]}`, nil), nil
	})}
	client, err := New("https://cloud.example", httpClient)
	if err != nil {
		t.Fatal(err)
	}
	page, err := client.ListResources(context.Background(), "fixture-access", ResourceQuery{ResourceType: "skill", Cursor: "next", PageSize: 20})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].ResourceName != "A" {
		t.Fatalf("page = %+v", page)
	}
}

func TestGetAndAuthorizeResourceDownloadPreservesETag(t *testing.T) {
	call := 0
	httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		call++
		switch call {
		case 1:
			return jsonResponse(http.StatusOK, `{"resource_id":"resource-1","resource_type":"workflow","client_resource_key":"workflow:a","resource_name":"A","content_hash":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","content_size":10,"format_schema":"lazymind.resource-manifest/v2","updated_at":"2026-08-20T10:00:00+08:00"}`, map[string]string{"ETag": `"revision-1"`}), nil
		case 2:
			if request.Method != http.MethodPost || request.URL.Path != "/v1/resources/resource-1/download-authorizations" {
				t.Fatalf("request = %s %s", request.Method, request.URL.Path)
			}
			if request.Header.Get("If-Match") != `"revision-1"` {
				t.Fatalf("If-Match = %q", request.Header.Get("If-Match"))
			}
			return jsonResponse(http.StatusAccepted, `{"status":"preparing","download_id":"download-1"}`, map[string]string{"Retry-After": "3"}), nil
		default:
			t.Fatalf("unexpected call %d", call)
			return nil, nil
		}
	})}
	client, err := New("https://cloud.example", httpClient)
	if err != nil {
		t.Fatal(err)
	}
	resource, etag, err := client.GetResource(context.Background(), "fixture-access", "resource-1")
	if err != nil || resource.ResourceID != "resource-1" || etag != `"revision-1"` {
		t.Fatalf("resource=%+v etag=%q err=%v", resource, etag, err)
	}
	authorization, err := client.AuthorizeResourceDownload(context.Background(), "fixture-access", resource.ResourceID, etag)
	if err != nil {
		t.Fatal(err)
	}
	if authorization.Status != DownloadPreparing || authorization.DownloadID != "download-1" || authorization.RetryAfterSeconds != 3 {
		t.Fatalf("authorization = %+v", authorization)
	}
}
