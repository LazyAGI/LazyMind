package cloudclient

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

type CloudError struct {
	Code              int    `json:"code"`
	Message           string `json:"message"`
	RequestID         string `json:"request_id"`
	Retryable         bool   `json:"retryable"`
	RetryAfterSeconds *int   `json:"retry_after_seconds"`
	HTTPStatus        int    `json:"-"`
}

func (e *CloudError) Error() string {
	return fmt.Sprintf("LazyMind Cloud request failed: status=%d code=%d request_id=%s", e.HTTPStatus, e.Code, e.RequestID)
}

func validateBearer(accessToken string) error {
	if strings.TrimSpace(accessToken) == "" {
		return errors.New("LazyMind Cloud access token is required")
	}
	return nil
}

func setCloudHeaders(request *http.Request, accessToken string) {
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer "+strings.TrimSpace(accessToken))
}

func decodeStrictJSON(response *http.Response, target any) error {
	decoder := json.NewDecoder(response.Body)
	decoder.DisallowUnknownFields()
	return decoder.Decode(target)
}

func decodeCloudError(response *http.Response) error {
	var cloudErr CloudError
	_ = json.NewDecoder(response.Body).Decode(&cloudErr)
	cloudErr.HTTPStatus = response.StatusCode
	return &cloudErr
}

// doJSON executes an already-authorized request without adding credentials.
// Bounded resource reads, signed object transfers and credential-restore
// decoding retain their separate validation and transport rules.
func (c *Client) doJSON(request *http.Request, expectedStatus int, output any, decodeContext string) error {
	response, err := c.httpClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != expectedStatus {
		return decodeCloudError(response)
	}
	if output == nil || expectedStatus == http.StatusNoContent {
		return nil
	}
	if err := decodeStrictJSON(response, output); err != nil {
		return fmt.Errorf("%s: %w", decodeContext, err)
	}
	return nil
}
