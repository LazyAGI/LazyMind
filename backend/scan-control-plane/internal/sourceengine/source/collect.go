package source

import store "github.com/lazymind/scan_control_plane/internal/store/source"

func collectBindings(prepared []preparedBinding) []store.Binding {
	bindings := make([]store.Binding, 0, len(prepared))
	for _, item := range prepared {
		bindings = append(bindings, item.binding)
	}
	return bindings
}

func collectCheckpoints(prepared []preparedBinding) []store.SyncCheckpoint {
	checkpoints := make([]store.SyncCheckpoint, 0, len(prepared))
	for _, item := range prepared {
		checkpoints = append(checkpoints, item.checkpoint)
	}
	return checkpoints
}

func bindingIDsJSON(prepared []preparedBinding) store.JSON {
	values := make([]any, 0, len(prepared))
	for _, item := range prepared {
		values = append(values, item.binding.BindingID)
	}
	return store.JSON{"items": values}
}

func folderIDsJSON(prepared []preparedBinding) store.JSON {
	values := make([]any, 0, len(prepared))
	for _, item := range prepared {
		values = append(values, item.binding.CoreParentDocumentID)
	}
	return store.JSON{"items": values}
}

func jobErrorsJSON(errors []JobError) store.JSON {
	values := make([]any, 0, len(errors))
	for _, item := range errors {
		values = append(values, syncJobErrorJSON(item))
	}
	return store.JSON{"items": values}
}

func syncJobErrorJSON(item JobError) store.JSON {
	return store.JSON{
		"code":    item.Code,
		"message": item.Message,
		"details": item.Details,
	}
}
