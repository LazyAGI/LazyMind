package workflow

import "errors"

// Retain the missing-capability marker referenced by the frozen contract tests.
var errDocumentPublicationNotImplemented = errors.New("document publication transactions not implemented")
