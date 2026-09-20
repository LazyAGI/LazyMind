package credentialvault

import (
	"context"
	"net/http"
	"sync"

	"lazymind/core/common"
)

var defaultBackupServiceState = struct {
	sync.RWMutex
	service *BackupService
}{}

func SetDefaultBackupService(service *BackupService) {
	defaultBackupServiceState.Lock()
	defaultBackupServiceState.service = service
	defaultBackupServiceState.Unlock()
}

func DefaultBackupService() *BackupService {
	defaultBackupServiceState.RLock()
	defer defaultBackupServiceState.RUnlock()
	return defaultBackupServiceState.service
}

type BackupHandler struct{ Service *BackupService }

func (handler BackupHandler) Status(w http.ResponseWriter, r *http.Request) {
	if handler.Service == nil {
		common.ReplyOK(w, BackupStatus{ReasonCode: "credential_backup_unavailable"})
		return
	}
	status, err := handler.Service.Status(r.Context())
	if err != nil {
		common.ReplyErr(w, "credential backup status is unavailable", http.StatusServiceUnavailable)
		return
	}
	common.ReplyOK(w, status)
}

func (handler BackupHandler) Enable(w http.ResponseWriter, r *http.Request) {
	if handler.Service == nil {
		common.ReplyErr(w, "credential backup is unavailable", http.StatusServiceUnavailable)
		return
	}
	status, err := handler.Service.Enable(r.Context())
	if err != nil {
		common.ReplyErr(w, "credential backup could not be enabled", http.StatusServiceUnavailable)
		return
	}
	common.ReplyOK(w, status)
}

func (handler BackupHandler) Disable(w http.ResponseWriter, r *http.Request) {
	if handler.Service == nil {
		common.ReplyErr(w, "credential backup is unavailable", http.StatusServiceUnavailable)
		return
	}
	status, err := handler.Service.Disable(r.Context())
	if err != nil {
		common.ReplyErr(w, "credential backup could not be disabled", http.StatusServiceUnavailable)
		return
	}
	common.ReplyOK(w, status)
}

func RunDefaultBackupWorker(ctx context.Context) {
	if service := DefaultBackupService(); service != nil {
		service.Run(ctx, 0)
	}
}
