package localworkspace

import "lazymind/core/common"

func Error(reason string, status int, catalogMessage string) *common.AppError {
	return common.ResolveAppError(catalogMessage, status).
		WithDetail(map[string]any{"reason": reason})
}

func ModeError() *common.AppError {
	return Error("mode_forbidden", 403, "forbidden")
}
