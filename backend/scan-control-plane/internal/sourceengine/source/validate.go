package source

import (
	"strconv"
	"strings"

	scheduleengine "github.com/lazymind/scan_control_plane/internal/sourceengine/schedule"
)

func validateSourceName(name string) error {
	if strings.TrimSpace(name) == "" {
		return FieldError("name", "required")
	}
	return nil
}

func validateCreateRequest(req CreateSourceRequest) error {
	if strings.TrimSpace(req.CallerID) == "" {
		return FieldError("caller_id", "required")
	}
	if strings.TrimSpace(req.RequestID) == "" {
		return FieldError("request_id", "required")
	}
	if err := validateSourceName(req.Name); err != nil {
		return err
	}
	if len(req.Bindings) == 0 {
		return FieldError("bindings", "at least one binding is required")
	}
	for i, binding := range req.Bindings {
		if err := validateBindingInput(binding, true); err != nil {
			return fieldWrap(i, err)
		}
	}
	return nil
}

func validateBindingInput(input BindingInput, targetRequired bool) error {
	if input.SyncMode == "" {
		return FieldError("sync_mode", "required")
	}
	if input.SyncMode != SyncModeManual && input.SyncMode != SyncModeScheduled && input.SyncMode != SyncModeWatch {
		return FieldError("sync_mode", "unsupported")
	}
	if input.SyncMode == SyncModeScheduled && len(input.SchedulePolicy) == 0 {
		return FieldError("schedule_policy", "required for scheduled sync")
	}
	if input.SyncMode == SyncModeScheduled {
		if err := scheduleengine.ValidateSchedulePolicy(input.SchedulePolicy); err != nil {
			return FieldError("schedule_policy", err.Error())
		}
	}
	if targetRequired {
		if input.ConnectorType == "" {
			return FieldError("connector_type", "required")
		}
		if input.TargetType == "" {
			return FieldError("target_type", "required")
		}
		if strings.TrimSpace(input.TargetRef) == "" {
			return FieldError("target_ref", "required")
		}
	}
	if input.Status != "" && input.Status != BindingStatusActive && input.Status != BindingStatusPaused {
		return FieldError("status", "unsupported")
	}
	return nil
}

func fieldWrap(index int, err error) error {
	engineErr, ok := err.(*EngineError)
	if !ok || engineErr.Details == nil {
		return err
	}
	if field, ok := engineErr.Details["field"].(string); ok {
		engineErr.Details["field"] = "bindings[" + strconv.Itoa(index) + "]." + field
	}
	return engineErr
}
