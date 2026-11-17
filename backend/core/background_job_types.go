package main

import (
	"lazymind/core/academic"
	"lazymind/core/chat"
	"lazymind/core/doc"
	"lazymind/core/documentdirectory"
	"lazymind/core/vocabulary"
)

func defaultAsyncJobExcludedTypes(ragActive bool) []string {
	excludedJobs := append([]string{doc.ReadingPreparationJob, vocabulary.CloudSyncJobType, documentdirectory.PlanJob, documentdirectory.OrganizeJob, academic.ChatLaunchJobType, academic.PreparationJobType}, chat.ConversationTitleJobTypes...)
	if !ragActive {
		excludedJobs = append(excludedJobs, doc.MarketInstallJobType, doc.MarketUpdateJobType, doc.MarketUpdateAllJobType)
	}
	return excludedJobs
}
