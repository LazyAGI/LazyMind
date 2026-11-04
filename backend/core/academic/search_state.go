package academic

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"lazymind/core/academic/arxivsource"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/modelconfig"
	"lazymind/core/state"
	"lazymind/core/store"
)

type searchFailure struct {
	Status  string     `json:"status"`
	RetryAt *time.Time `json:"retry_at,omitempty"`
}

func (e *searchFailure) Error() string { return e.Status }

func configureSearchSource(ctx context.Context, user, source string, request *paperSearchRequest) error {
	var err error
	switch source {
	case "openalex":
		request.apiKey, err = modelconfig.LoadOpenAlexAPIKey(ctx, store.DB(), user)
	case "sciverse", "web":
		capability := "academic_search"
		if source == "web" {
			capability = "web_search"
		}
		request.toolConfig, err = modelconfig.LoadToolConfigForCapabilities(ctx, store.DB(), user, []string{capability})
	}
	if err != nil {
		return err
	}
	request.scope = sourceHash(map[string]any{"source": source, "key": request.apiKey, "tools": request.toolConfig})
	if (source == "sciverse" && request.toolConfig["sciverse"] == nil) || (source == "web" && len(request.toolConfig) == 0) {
		return &searchFailure{Status: "missing_configuration"}
	}
	return nil
}

func scholarlyHTTPFailure(status int, headers http.Header, body io.Reader) *searchFailure {
	failure := statusSearchFailure(status)
	var detail struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	_ = json.NewDecoder(io.LimitReader(body, 64<<10)).Decode(&detail)
	if status == http.StatusTooManyRequests {
		message := strings.ToLower(detail.Error + " " + detail.Message)
		if headers.Get("X-RateLimit-Remaining") == "0" || strings.Contains(message, "credits") || strings.Contains(message, "daily") || strings.Contains(message, "budget") || strings.Contains(message, "quota") {
			failure.Status = "quota_exhausted"
		}
	}
	if failure.Status == "rate_limited" || failure.Status == "quota_exhausted" {
		now := time.Now().UTC()
		if seconds, err := strconv.Atoi(headers.Get("Retry-After")); err == nil && seconds > 0 {
			until := now.Add(time.Duration(seconds) * time.Second)
			failure.RetryAt = &until
		} else if until, err := http.ParseTime(headers.Get("Retry-After")); err == nil && until.After(now) {
			failure.RetryAt = &until
		} else if failure.Status == "quota_exhausted" {
			until := now.Truncate(24 * time.Hour).Add(24 * time.Hour)
			if seconds, err := strconv.Atoi(headers.Get("X-RateLimit-Reset")); err == nil && seconds > 0 {
				until = now.Add(time.Duration(seconds) * time.Second)
			}
			failure.RetryAt = &until
		}
	}
	return failure
}

func statusSearchFailure(status int) *searchFailure {
	failure := &searchFailure{Status: "failed"}
	switch status {
	case 401, 403:
		failure.Status = "authentication_failed"
	case 402:
		failure.Status = "quota_exhausted"
	case 429:
		failure.Status = "rate_limited"
	}
	return failure
}

func classifySearchFailure(err error) *searchFailure {
	var failure *searchFailure
	if errors.As(err, &failure) {
		return failure
	}
	var remote *common.HTTPError
	if errors.As(err, &remote) {
		failure = statusSearchFailure(remote.StatusCode)
		var envelope struct {
			Detail json.RawMessage `json:"detail"`
		}
		if json.Unmarshal(remote.Body, &envelope) == nil {
			var detail struct {
				Code    string     `json:"code"`
				RetryAt *time.Time `json:"retry_at"`
			}
			if json.Unmarshal(envelope.Detail, &detail) == nil {
				switch detail.Code {
				case "rate_limited", "quota_exhausted", "authentication_failed", "missing_configuration":
					failure.Status = detail.Code
				}
				failure.RetryAt = detail.RetryAt
			} else if string(envelope.Detail) == `"missing_configuration"` {
				failure.Status = "missing_configuration"
			}
		}
		return failure
	}
	var arxiv *arxivsource.LookupError
	if errors.As(err, &arxiv) {
		return scholarlyHTTPFailure(arxiv.StatusCode, http.Header{"Retry-After": []string{arxiv.RetryAfter}}, strings.NewReader(""))
	}
	return &searchFailure{Status: "failed"}
}

func searchCooldown(ctx context.Context, scope string) (*searchFailure, error) {
	raw, err := store.State().Get(ctx, "academic/search/cooldown/"+scope)
	if state.IsMissing(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var failure searchFailure
	if err := json.Unmarshal(raw, &failure); err != nil {
		return nil, err
	}
	return &failure, nil
}

func saveSearchCooldown(ctx context.Context, scope string, failure *searchFailure) error {
	now := time.Now().UTC()
	if failure.RetryAt == nil {
		var delay time.Duration
		switch failure.Status {
		case "rate_limited":
			delay = 30 * time.Second
		case "authentication_failed":
			delay = 15 * time.Minute
		case "quota_exhausted":
			delay = now.Truncate(24 * time.Hour).Add(24 * time.Hour).Sub(now)
		default:
			return nil
		}
		until := now.Add(delay)
		failure.RetryAt = &until
	}
	if !failure.RetryAt.After(now) {
		return nil
	}
	return store.State().Set(ctx, "academic/search/cooldown/"+scope, marshal(failure), time.Until(*failure.RetryAt))
}

func failureOutcome(source string, failure *searchFailure) searchOutcome {
	messages := map[string]string{
		"failed":                "来源请求失败，本轮可使用其他来源继续检索",
		"missing_configuration": "该来源尚未配置访问凭证",
		"authentication_failed": "该来源鉴权失败，请检查访问凭证",
		"rate_limited":          "该来源暂时限流，恢复前使用其他来源",
		"quota_exhausted":       "该来源额度已耗尽，恢复前使用其他来源",
	}
	return searchOutcome{Source: source, Status: failure.Status, Limitation: messages[failure.Status], RetryAt: failure.RetryAt}
}

type searchCursor struct {
	ExpansionID string `json:"expansion_id"`
	Revision    int64  `json:"revision"`
	Key         string `json:"key"`
}

func nextSearchCursor(record orm.AcademicExpansion, key string) string {
	return base64.RawURLEncoding.EncodeToString(marshal(searchCursor{record.ID, record.PreparationRevision, key}))
}

func resumeSearch(record orm.AcademicExpansion, request *paperSearchRequest, searches []paperDiscovery) error {
	var cursor searchCursor
	data, err := base64.RawURLEncoding.DecodeString(request.Cursor)
	if err != nil || json.Unmarshal(data, &cursor) != nil || cursor.ExpansionID != record.ID || cursor.Revision != record.PreparationRevision {
		return errors.New("invalid or outdated search cursor")
	}
	for _, saved := range searches {
		if saved.CacheKey != cursor.Key || saved.Next == nil || len(saved.Sources) != 1 {
			continue
		}
		if (request.Query != "" && request.Query != saved.Query) || (request.Mode != "" && request.Mode != saved.Mode) || (request.PaperID != "" && request.PaperID != saved.PaperID) || (len(request.Sources) > 0 && (len(request.Sources) != 1 || request.Sources[0] != saved.Sources[0].Source)) || (request.MaxResults != 0 && request.MaxResults != saved.MaxResults) {
			return errors.New("search cursor does not match supplied search conditions")
		}
		request.Query, request.Mode, request.PaperID = saved.Query, saved.Mode, saved.PaperID
		request.MaxResults, request.Sources, request.position = saved.MaxResults, []string{saved.Sources[0].Source}, *saved.Next
		return nil
	}
	return errors.New("search cursor no longer belongs to a saved result")
}
