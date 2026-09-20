package knowledgeplaza

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lazymind/core/cloudclient"
)

type fakeSource struct {
	page  cloudclient.KnowledgePage
	err   error
	query cloudclient.KnowledgeQuery
}

func (f *fakeSource) List(_ context.Context, query cloudclient.KnowledgeQuery) (cloudclient.KnowledgePage, error) {
	f.query = query
	return f.page, f.err
}

func TestListMapsCloudItemsToKnowledgeSquareDTO(t *testing.T) {
	source := &fakeSource{page: cloudclient.KnowledgePage{Items: []cloudclient.KnowledgeItem{{
		ID: "law", Type: "industry", Domain: "法律", Icon: "law", Name: "法规库",
		Description: "摘要", Tags: []string{"法律"}, DocumentCount: 10, DisplaySize: "20 MB",
		Version: "v1", Updated: "2026-08-20", Coverage: "公开范围", Source: "LazyMind Cloud",
	}}}}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/cloud/knowledge-square?q=law&type=industry&domain=法律&page=2&page_size=20", nil)

	Handler{Source: source}.List(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if source.query.Keyword != "law" || source.query.Type != "industry" || source.query.Domain != "法律" || source.query.Page != 2 || source.query.PageSize != 20 {
		t.Fatalf("query was not forwarded: %+v", source.query)
	}
	var response struct {
		Data struct {
			Items []cloudclient.KnowledgeItem `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Data.Items) != 1 || response.Data.Items[0].ID != "cloud:law" {
		t.Fatalf("Cloud item was not namespaced: %+v", response.Data.Items)
	}
	if response.Data.Items[0].Questions == nil {
		t.Fatal("questions must be an empty array, not null")
	}
}

func TestListDoesNotExposeSourceError(t *testing.T) {
	source := &fakeSource{err: errors.New("upstream failed with token secret-canary")}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/cloud/knowledge-square", nil)

	Handler{Source: source}.List(recorder, request)

	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if body := recorder.Body.String(); body == "" || strings.Contains(body, "secret-canary") {
		t.Fatalf("unsafe error response: %s", body)
	}
}

func TestListRejectsInvalidPaginationBeforeCallingCloud(t *testing.T) {
	source := &fakeSource{}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/cloud/knowledge-square?page_size=101", nil)

	Handler{Source: source}.List(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}
