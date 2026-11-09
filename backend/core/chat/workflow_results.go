package chat

import (
	"errors"

	"lazymind/core/common/orm"
	"lazymind/core/taskcenter"
)

var errExternalWorkflowConversation = errors.New("this conversation displays external workflow results; use the workflow controls or continue with the external agent")

func mergeExternalWorkflowConversationMetadata(item map[string]any, conversation orm.Conversation) {
	if sessionID := taskcenter.ExternalWorkflowSessionID(conversation); sessionID != "" {
		item["source_type"] = taskcenter.ExternalWorkflowConversationSource
		item["external_workflow_session_id"] = sessionID
		item["execution_owner"] = "external_agent"
	}
}
