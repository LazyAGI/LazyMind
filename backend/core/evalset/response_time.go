package evalset

import (
	"encoding/json"
	"time"
)

var responseTimeLocation = time.FixedZone("Asia/Shanghai", 8*60*60)

func formatResponseTime(value time.Time) string {
	return value.In(responseTimeLocation).Format(time.RFC3339Nano)
}

func formatOptionalResponseTime(value *time.Time) *string {
	if value == nil {
		return nil
	}
	formatted := formatResponseTime(*value)
	return &formatted
}

func (resp EvalSetResponse) MarshalJSON() ([]byte, error) {
	type Alias EvalSetResponse
	return json.Marshal(&struct {
		*Alias
		CreatedAt string `json:"created_at"`
		UpdatedAt string `json:"updated_at"`
	}{
		Alias:     (*Alias)(&resp),
		CreatedAt: formatResponseTime(resp.CreatedAt),
		UpdatedAt: formatResponseTime(resp.UpdatedAt),
	})
}

func (resp EvalSetItemResponse) MarshalJSON() ([]byte, error) {
	type Alias EvalSetItemResponse
	return json.Marshal(&struct {
		*Alias
		CreatedAt string `json:"created_at"`
		UpdatedAt string `json:"updated_at"`
	}{
		Alias:     (*Alias)(&resp),
		CreatedAt: formatResponseTime(resp.CreatedAt),
		UpdatedAt: formatResponseTime(resp.UpdatedAt),
	})
}

func (resp ImportPreviewResponse) MarshalJSON() ([]byte, error) {
	type Alias ImportPreviewResponse
	return json.Marshal(&struct {
		*Alias
		ExpiresAt string `json:"expires_at"`
	}{
		Alias:     (*Alias)(&resp),
		ExpiresAt: formatResponseTime(resp.ExpiresAt),
	})
}

func (resp EvalSetImportTaskResponse) MarshalJSON() ([]byte, error) {
	type Alias EvalSetImportTaskResponse
	return json.Marshal(&struct {
		*Alias
		CreatedAt  string  `json:"created_at"`
		StartedAt  *string `json:"started_at"`
		FinishedAt *string `json:"finished_at"`
	}{
		Alias:      (*Alias)(&resp),
		CreatedAt:  formatResponseTime(resp.CreatedAt),
		StartedAt:  formatOptionalResponseTime(resp.StartedAt),
		FinishedAt: formatOptionalResponseTime(resp.FinishedAt),
	})
}
