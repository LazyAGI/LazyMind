package cloudclient

import (
	"context"
	"net/http"
	"testing"
)

func TestListKnowledgeUsesApprovedPathAndBearer(t *testing.T) {
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != knowledgePlazaPath {
			t.Fatalf("path=%q", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer fixture-access" {
			t.Fatalf("authorization=%q", r.Header.Get("Authorization"))
		}
		if r.URL.Query().Get("q") != "law" || r.URL.Query().Get("page_size") != "20" {
			t.Fatalf("query=%q", r.URL.RawQuery)
		}
		return jsonResponse(http.StatusOK, `{"items":[{"id":"cloud-law","type":"industry","domain":"law","icon":"law","name":"Law","desc":"desc","tags":[],"docs":1,"size":"1 MB","version":"v1","updated":"2026-08-20","installed":false,"coverage":"public","source":"Cloud"}]}`, nil), nil
	})}
	client, err := New("https://cloud.example", httpClient)
	if err != nil {
		t.Fatal(err)
	}
	page, err := client.ListKnowledge(context.Background(), "fixture-access", KnowledgeQuery{Keyword: "law", PageSize: 20})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].ID != "cloud-law" {
		t.Fatalf("page=%+v", page)
	}
}

func TestListKnowledgeRejectsMissingAccessToken(t *testing.T) {
	client, err := New("https://cloud.example", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.ListKnowledge(context.Background(), "", KnowledgeQuery{}); err == nil {
		t.Fatal("missing access token accepted")
	}
}

func TestClientRequiresCloudOrigin(t *testing.T) {
	if _, err := New("https://cloud.example/base", nil); err == nil {
		t.Fatal("Cloud base URL with a path must be rejected")
	}
	client, err := New("https://cloud.example/", nil)
	if err != nil {
		t.Fatal(err)
	}
	if client.Origin() != "https://cloud.example" {
		t.Fatalf("origin = %q", client.Origin())
	}
}
