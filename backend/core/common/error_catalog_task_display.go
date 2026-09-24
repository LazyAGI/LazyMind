package common

import "net/http"

func init() {
	for _, source := range []string{"query task display failed", "unable to load task details"} {
		registerAdditionalErrorAlias(source, "Task details could not be loaded", http.StatusInternalServerError, 2003108)
	}
	for _, source := range []string{"display cursor expired", "task display changed; reload the task", "please reload task details", "stale task execution", "public event conflict"} {
		registerAdditionalErrorAlias(source, "Task details changed; reload the task", http.StatusConflict, 2003109)
	}
	for _, source := range []string{"task event rejected", "invalid public process step", "invalid public display event"} {
		registerAdditionalErrorAlias(source, "Task event could not be accepted", http.StatusBadRequest, 2003110)
	}
	registerAdditionalErrorAlias("workflow session unavailable", "Workflow session unavailable", http.StatusNotFound, 2003111)
	registerAdditionalErrorAlias("sources snapshot exceeds limit", "Task sources exceed the supported limit", http.StatusBadRequest, 2003112)
	registerAdditionalErrorAlias("persist task step", "Task update could not be saved", http.StatusInternalServerError, 2003113)
}
