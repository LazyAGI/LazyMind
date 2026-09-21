package common

import "net/http"

func init() {
	registerAdditionalError("notes too long", http.StatusBadRequest, 2002930)
	registerAdditionalError("recording not found", http.StatusNotFound, 2002931)
	registerAdditionalError("recording cannot be retried", http.StatusConflict, 2002932)
	registerAdditionalError("recording evidence unavailable", http.StatusBadRequest, 2002933)
	registerAdditionalError("recording source unavailable", http.StatusBadRequest, 2002934)
	registerAdditionalError("recording evidence too large", http.StatusBadRequest, 2002935)
	registerAdditionalError("recording already running", http.StatusConflict, 2002936)
	registerAdditionalError("select a connected browser", http.StatusBadRequest, 2002937)
	registerAdditionalError("Browser recording unavailable. Connect the browser extension and grant page access.", http.StatusBadGateway, 2002938)
	registerAdditionalError("recording requires 2–120 frames", http.StatusBadRequest, 2002939)
	registerAdditionalError("recording no longer running", http.StatusConflict, 2002940)
	registerAdditionalError("recording is not pending", http.StatusConflict, 2002941)
	registerAdditionalError("recording already decided", http.StatusConflict, 2002942)
	registerAdditionalError("confirm the recorded skill before enabling it", http.StatusConflict, 2002943)
	registerAdditionalError("use the recording confirmation action to remove pending status", http.StatusConflict, 2002944)
}
