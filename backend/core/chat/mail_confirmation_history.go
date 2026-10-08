package chat

// History stores a recovery guard, never the patch or uploaded attachment bytes.
// This marker is not itself authorization to send or to replay a confirmation.
func mailConfirmationHistoryMetadata(raw map[string]any) map[string]any {
	id := resolveMailDraftConfirmID(raw)
	revision := resolveMailDraftConfirmRevision(raw)
	if id == "" || revision < 1 {
		return nil
	}
	return map[string]any{"mail_draft_confirm_id": id, "mail_draft_confirm_revision": revision}
}
