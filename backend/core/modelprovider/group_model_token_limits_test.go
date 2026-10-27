package modelprovider

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestModelTokenLimitsPersistAndClearIndependently(t *testing.T) {
	_, model := modelEditFixture(t, "llm", false)
	rec := modelEditCall(t, model.ID, "user-1", `{"max_input_tokens":"256K","input_token_limit":"200k","max_output_tokens":"32k"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("save=%d %s", rec.Code, rec.Body.String())
	}
	var result struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Data["max_input_tokens"] != "256K" || result.Data["input_token_limit"] != "200K" || result.Data["max_output_tokens"] != "32K" {
		t.Fatalf("limits were conflated or lost: %#v", result.Data)
	}
	rec = modelEditCall(t, model.ID, "user-1", `{"name":"renamed-with-limits"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("rename=%d %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Data["input_token_limit"] != "200K" || result.Data["max_output_tokens"] != "32K" {
		t.Fatalf("partial edit lost limits: %#v", result.Data)
	}
	rec = modelEditCall(t, model.ID, "user-1", `{"input_token_limit":"","max_output_tokens":""}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("clear=%d %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Data["input_token_limit"] != nil || result.Data["max_output_tokens"] != nil {
		t.Fatalf("clear did not restore automatic limits: %#v", result.Data)
	}
}

func TestModelTokenLimitsRejectInvalidOrOversizedValues(t *testing.T) {
	for _, body := range []string{
		`{"input_token_limit":"0"}`, `{"max_output_tokens":"1.5K"}`,
		`{"input_token_limit":"256K"}`, `{"max_output_tokens":"1M"}`,
		`{"max_input_tokens":"16K","input_token_limit":"32K"}`,
	} {
		t.Run(body, func(t *testing.T) {
			_, model := modelEditFixture(t, "llm", false)
			rec := modelEditCall(t, model.ID, "user-1", body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("invalid edit=%d %s", rec.Code, rec.Body.String())
			}
		})
	}
	_, model := modelEditFixture(t, "rerank", false)
	rec := modelEditCall(t, model.ID, "user-1", `{"input_token_limit":"16K"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("non-chat limits=%d %s", rec.Code, rec.Body.String())
	}
}
