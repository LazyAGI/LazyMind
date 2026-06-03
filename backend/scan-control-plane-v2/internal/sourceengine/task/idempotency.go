package task

import (
	"fmt"

	store "github.com/lazymind/scan_control_plane/internal/store/source"
)

func IdempotencyKey(task store.ParseTask) string {
	return fmt.Sprintf("%s:%s:%s:%s:%s:%d",
		task.SourceID,
		task.BindingID,
		task.ObjectKey,
		task.TargetVersionID,
		task.TaskAction,
		task.BindingGeneration,
	)
}
