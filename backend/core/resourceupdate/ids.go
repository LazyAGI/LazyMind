package resourceupdate

import (
	"strings"

	"lazymind/core/common"
)

const skillReviewRequestIDPrefix = "review_"

func newSkillReviewRequestID() string {
	return skillReviewRequestIDPrefix + common.GenerateID()
}

func normalizeSkillReviewRequestID(requestID string) string {
	requestID = strings.TrimSpace(requestID)
	if requestID == "" || strings.HasPrefix(requestID, skillReviewRequestIDPrefix) {
		return requestID
	}
	return skillReviewRequestIDPrefix + requestID
}
