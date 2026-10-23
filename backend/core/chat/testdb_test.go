package chat

import (
	"testing"

	"lazymind/core/common/orm"
)

// Conversation lifecycle includes grouping, workspace and model-policy dependencies.
func newConversationTestDB(t *testing.T) *orm.DB {
	t.Helper()
	return orm.MigrateTestDB(t,
		&orm.AsyncJob{}, &orm.ChatHistory{}, &orm.Conversation{},
		&orm.ConversationForkOrigin{}, &orm.ConversationGroup{}, &orm.ConversationGroupMember{},
		&orm.ConversationGroupState{}, &orm.ConversationOpening{}, &orm.ConversationOrganizerRun{},
		&orm.ConversationOrganizerSnapshotItem{}, &orm.ConversationToolGrant{}, &orm.ConversationWorkspaceBinding{},
		&orm.ExternalAgentBinding{}, &orm.ExternalAgentSession{}, &orm.LocalWorkspace{},
		&orm.TaskCenterTask{}, &orm.UserChatSettings{}, &orm.UserModelProvider{},
		&orm.UserModelProviderGroup{}, &orm.UserModelProviderGroupModel{}, &orm.UserSelectedCloudModel{},
		&orm.UserSelectedModel{},
	)
}

// Model and settings handlers also inspect active conversation executions.
func newChatModelTestDB(t *testing.T) *orm.DB {
	t.Helper()
	return orm.MigrateTestDB(t,
		&orm.ChatHistory{}, &orm.MultiAnswersChatHistory{}, &orm.Conversation{},
		&orm.ExternalAgentBinding{}, &orm.ExternalChatHost{}, &orm.ExternalChatRun{},
		&orm.SubAgentTask{}, &orm.TaskAttemptTiming{}, &orm.TaskCenterTask{},
		&orm.TaskExecutionTiming{}, &orm.UserChatSettings{}, &orm.UserModelProvider{},
		&orm.UserModelProviderGroup{}, &orm.UserModelProviderGroupModel{}, &orm.UserSelectedCloudModel{},
		&orm.UserSelectedModel{}, &orm.UserSelectedProvider{}, &orm.WorkflowSession{},
	)
}

// Notification reconciliation reads real task, workflow and history state.
func newChatNotificationTestDB(t *testing.T) *orm.DB {
	t.Helper()
	return orm.MigrateTestDB(t,
		&orm.ChatHistory{}, &orm.ChatRunPerformance{}, &orm.Conversation{},
		&orm.ConversationArtifact{}, &orm.DesktopNotificationReceipt{}, &orm.ExternalChatRun{},
		&orm.MultiAnswersChatHistory{}, &orm.ScheduleDependency{}, &orm.SubAgentArtifact{},
		&orm.SubAgentTask{}, &orm.TaskAttemptTiming{}, &orm.TaskCenterTask{},
		&orm.TaskExecutionTiming{}, &orm.TaskNotification{}, &orm.TaskRunInput{},
		&orm.TaskRunOutput{}, &orm.UserModelProviderGroup{}, &orm.UserModelProviderGroupModel{},
		&orm.UserNotificationPreferences{}, &orm.UserSchedule{}, &orm.UserSelectedCloudModel{},
		&orm.UserSelectedModel{}, &orm.UserUIPreferences{}, &orm.WorkflowEvent{},
		&orm.WorkflowSession{}, &orm.WorkflowSessionStep{},
	)
}

func newChatExecutionTestDB(t *testing.T) *orm.DB {
	t.Helper()

	// Chat execution traverses configuration, tasks, notifications and artifacts.
	// Keep downstream tables present even when the scenario seeds no rows there.
	return orm.MigrateTestDB(t,
		&orm.Conversation{}, &orm.ChatHistory{}, &orm.MultiAnswersChatHistory{}, &orm.ChatRunPerformance{},
		&orm.ConversationForkOrigin{}, &orm.ConversationForkRequest{}, &orm.ConversationOpening{},
		&orm.ConversationArchiveFolder{}, &orm.ConversationIdleEvent{},
		&orm.ConversationGroup{}, &orm.ConversationGroupMember{}, &orm.ConversationGroupState{},
		&orm.ConversationOrganizerRun{}, &orm.ConversationOrganizerSnapshotItem{},
		&orm.LocalWorkspace{}, &orm.ConversationWorkspaceBinding{}, &orm.ConversationToolGrant{},
		&orm.ExternalAgentBinding{}, &orm.ExternalAgentSession{},
		&orm.ExternalChatRun{}, &orm.ExternalChatRunEvent{}, &orm.ExternalChatHost{},
		&orm.SubAgentTask{}, &orm.SubAgentArtifact{}, &orm.AgentInvocation{},
		&orm.ConversationArtifact{}, &orm.ArtifactV2{}, &orm.ArtifactBlob{}, &orm.ArtifactBinding{},
		&orm.WorkflowSession{}, &orm.WorkflowSessionStep{}, &orm.WorkflowEvent{}, &orm.WorkflowTransitionCommand{},
		&orm.WorkflowResource{}, &orm.WorkflowRevision{}, &orm.WorkflowHumanArtifact{}, &orm.WorkflowSlotRevision{},
		&orm.TaskCenterTask{}, &orm.TaskExecutionTiming{}, &orm.TaskAttemptTiming{},
		&orm.TaskNotification{}, &orm.UserNotificationPreferences{}, &orm.UserSchedule{},
		&orm.TaskRunInput{}, &orm.TaskRunOutput{}, &orm.AsyncJob{},
		&orm.UserModelProvider{}, &orm.UserModelProviderGroup{}, &orm.UserModelProviderGroupModel{},
		&orm.UserSelectedModel{}, &orm.UserSelectedProvider{}, &orm.UserSelectedCloudModel{},
		&orm.UserChatSettings{}, &orm.UserUIPreferences{}, &orm.UserEnvironmentVariable{},
		&orm.UserPersonalizationSetting{}, &orm.UserWorkflowSetting{}, &orm.UserDisabledTool{},
		&orm.MCPServer{}, &orm.SkillV2Skill{}, &orm.SkillV2Draft{}, &orm.EpisodeMemory{},
		&orm.Dataset{}, &orm.DatasetUserState{}, &orm.Document{}, &orm.DocumentProcessingState{},
		&orm.ACLModel{}, &orm.KBModel{}, &orm.UserGroupModel{}, &orm.VisibilityModel{},
		&orm.UploadSession{}, &orm.UploadedFile{},
	)
}
