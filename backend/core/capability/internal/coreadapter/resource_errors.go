package coreadapter

import (
	"errors"
	"lazymind/core/capability"
	"lazymind/core/doc"
	"lazymind/core/log"
)

func mapResourceError(operation string, err error) error {
	if err == nil {
		return nil
	}
	var resource *doc.ResourceError
	if errors.As(err, &resource) {
		mapped := capability.NewError(capability.ErrorCode(resource.Code), operation, resource.Message, false, err)
		mapped.Details = resource.Details
		return mapped
	}
	var catalog *doc.DatasetServiceError
	if errors.As(err, &catalog) {
		return mapDatasetError(operation, err)
	}
	log.Logger.Error().Err(err).Str("operation", operation).Msg("knowledge resource operation failed")
	return mapDocumentError(operation, err)
}
