package tree

import "strconv"

func defaultLimits(limits TreeQueryLimits) TreeQueryLimits {
	if limits.DefaultPageSize <= 0 {
		limits.DefaultPageSize = 50
	}
	if limits.MaxPageSize <= 0 {
		limits.MaxPageSize = 100
	}
	if limits.MaxAllCurrentLevelItems <= 0 {
		limits.MaxAllCurrentLevelItems = 1000
	}
	return limits
}

func normalizePageSize(pageSize int, limits TreeQueryLimits) int {
	if pageSize <= 0 {
		return limits.DefaultPageSize
	}
	if pageSize > limits.MaxPageSize {
		return limits.MaxPageSize
	}
	return pageSize
}

func validateListMode(listMode, cursor string, maxItems int, limits TreeQueryLimits) (string, error) {
	if listMode == "" {
		listMode = ListModePage
	}
	switch listMode {
	case ListModePage:
		return listMode, nil
	case ListModeAllCurrentLevel:
		if cursor != "" {
			return "", NewError(ErrCodeInvalidRequest, "cursor must be empty for all_current_level")
		}
		if maxItems <= 0 {
			return "", NewError(ErrCodeInvalidRequest, "max_items is required for all_current_level")
		}
		if maxItems > limits.MaxAllCurrentLevelItems {
			return "", &QueryError{Code: ErrCodeResultTooLarge, Message: "max_items exceeds service limit", Details: map[string]any{"max_items": limits.MaxAllCurrentLevelItems}}
		}
		return listMode, nil
	default:
		return "", NewError(ErrCodeUnsupportedListMode, "list_mode is not supported")
	}
}

func validateSearchListMode(listMode string) error {
	if listMode == "" || listMode == ListModePage {
		return nil
	}
	return NewError(ErrCodeUnsupportedListMode, "list_mode is not supported for search")
}

func cursorOffset(cursor string) (int, error) {
	if cursor == "" {
		return 0, nil
	}
	offset, err := strconv.Atoi(cursor)
	if err != nil || offset < 0 {
		return 0, NewError(ErrCodeInvalidRequest, "cursor is invalid")
	}
	return offset, nil
}
