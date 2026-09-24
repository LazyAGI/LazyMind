package artifact

import "errors"

var (
	ErrNotFound            = errors.New("ARTIFACT_NOT_FOUND")
	ErrRevisionConflict    = errors.New("ARTIFACT_REVISION_CONFLICT")
	ErrIdempotencyConflict = errors.New("ARTIFACT_IDEMPOTENCY_CONFLICT")
	ErrBlobHashMismatch    = errors.New("ARTIFACT_BLOB_HASH_MISMATCH")
	ErrAccessDenied        = errors.New("ARTIFACT_ACCESS_DENIED")
	ErrQuotaExceeded       = errors.New("ARTIFACT_QUOTA_EXCEEDED")
	ErrDisabled            = errors.New("ARTIFACT_V2_DISABLED")
	ErrImmutableRevision   = errors.New("artifact revision payload is immutable")
)
