package coreadapter

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"lazymind/core/capability"
	"lazymind/core/doc"
)

func mapContextError(operation string, err error) error {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return capability.NewError(capability.DeadlineExceeded, operation, "deadline exceeded", true, err)
	case errors.Is(err, context.Canceled):
		return capability.NewError(capability.DeadlineExceeded, operation, "request canceled", true, err)
	default:
		return nil
	}
}

func mapSkillError(operation string, err error) error {
	if err == nil {
		return nil
	}
	var capabilityErr *capability.Error
	if errors.As(err, &capabilityErr) {
		return err
	}
	if mapped := mapContextError(operation, err); mapped != nil {
		return mapped
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return capability.NewError(capability.NotFound, operation, "skill not found", false, err)
	}
	return capability.NewError(capability.Internal, operation, "internal error", false, err)
}

func mapDatasetError(operation string, err error) error {
	if err == nil {
		return nil
	}
	var capabilityErr *capability.Error
	if errors.As(err, &capabilityErr) {
		return err
	}
	if mapped := mapContextError(operation, err); mapped != nil {
		return mapped
	}
	var serviceErr *doc.DatasetServiceError
	if errors.As(err, &serviceErr) {
		switch serviceErr.Code {
		case doc.DatasetServiceInvalidArgument:
			return capability.NewError(capability.InvalidArgument, operation, serviceErr.Message, false, err)
		case doc.DatasetServiceNotFound, doc.DatasetServiceForbidden:
			return capability.NewError(capability.NotFound, operation, "knowledge not found", false, err)
		case doc.DatasetServiceUnavailable:
			return capability.NewError(capability.Unavailable, operation, "backend unavailable", true, err)
		default:
			return capability.NewError(capability.Internal, operation, "internal error", false, err)
		}
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return capability.NewError(capability.NotFound, operation, "knowledge not found", false, err)
	}
	return capability.NewError(capability.Internal, operation, "internal error", false, err)
}
