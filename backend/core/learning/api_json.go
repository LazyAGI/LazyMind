package learning

import "encoding/json"

func (v Book) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]any{"id": v.ID, "name": v.Name, "description": v.Description, "capability_key": v.CapabilityKey, "capability_version": v.CapabilityVersion, "schema_version": v.SchemaVersion, "question_types_json": v.QuestionTypesJSON, "generation_policy_json": v.GenerationPolicyJSON, "archived_at": v.ArchivedAt, "created_at": v.CreatedAt, "updated_at": v.UpdatedAt})
}
func (v Content) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]any{"id": v.ID, "subject_id": v.SubjectID, "occurrence_id": v.OccurrenceID, "capability_key": v.CapabilityKey, "capability_version": v.CapabilityVersion, "schema_version": v.SchemaVersion, "content_json": v.ContentJSON, "origin": v.Origin, "status": v.Status, "provider_trace_id": v.ProviderTraceID, "model_config_id": v.ModelConfigID, "generator_version": v.GeneratorVersion, "user_edited": v.UserEdited, "created_at": v.CreatedAt, "updated_at": v.UpdatedAt})
}
func (v QuestionInstance) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]any{"id": v.ID, "card_id": v.CardID, "session_id": v.SessionID, "question_type": v.QuestionType, "question_type_version": v.QuestionTypeVersion, "payload_json": v.PayloadJSON, "explanation_json": v.ExplanationJSON, "locale": v.Locale, "generator_type": v.GeneratorType, "generator_version": v.GeneratorVersion, "content_version": v.ContentVersion, "status": v.Status, "created_at": v.CreatedAt})
}
func (v Card) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]any{"id": v.ID, "book_id": v.BookID, "book_entry_id": v.BookEntryID, "content_id": v.ContentID, "question_type": v.QuestionType, "scheduler_version": v.SchedulerVersion, "row_version": v.RowVersion, "suspended": v.Suspended, "created_at": v.CreatedAt, "updated_at": v.UpdatedAt})
}
func (v ReviewSession) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]any{"id": v.ID, "book_id": v.BookID, "locale": v.Locale, "status": v.Status, "total": v.Total, "answered": v.Answered, "correct": v.Correct, "created_at": v.CreatedAt, "completed_at": v.CompletedAt})
}
func (v PreanalysisTask) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]any{"id": v.ID, "dataset_id": v.DatasetID, "document_id": v.DocumentID, "document_revision": v.DocumentRevision, "status": v.Status, "capability_keys_json": v.CapabilityKeysJSON, "result_json": v.ResultJSON, "error_message": v.ErrorMessage, "total": v.Total, "completed": v.Completed, "failed": v.Failed, "created_at": v.CreatedAt, "started_at": v.StartedAt, "completed_at": v.CompletedAt, "updated_at": v.UpdatedAt})
}
