package handler

import (
	"errors"
	"net/http"
	"strings"

	"gorm.io/gorm"

	"lazymind/core/common"
	skillbuiltin "lazymind/core/skillv2/builtin"
	skilldistribution "lazymind/core/skillv2/distribution"
	skillservice "lazymind/core/skillv2/service"
)

func DistributionUpgradeStatus(w http.ResponseWriter, r *http.Request) {
	db, ok := requireDB(w)
	if !ok {
		return
	}
	userID, _, ok := requireUser(w, r)
	if !ok {
		return
	}
	skillID := strings.TrimSpace(common.PathVar(r, "skill_id"))
	status, err := newDistributionService(db).GetStatus(r.Context(), skilldistribution.StatusRequest{SkillID: skillID, UserID: userID})
	if err != nil {
		replyServiceError(w, err)
		return
	}
	common.ReplyOK(w, status)
}

func PrepareDistributionUpgrade(w http.ResponseWriter, r *http.Request) {
	db, ok := requireDB(w)
	if !ok {
		return
	}
	userID, _, ok := requireUser(w, r)
	if !ok {
		return
	}
	skillID := strings.TrimSpace(common.PathVar(r, "skill_id"))
	response, err := newDistributionService(db).Prepare(r.Context(), skilldistribution.PrepareRequest{SkillID: skillID, UserID: userID, DownloadPackage: true})
	if err != nil {
		if errors.Is(err, skilldistribution.ErrPackageUnavailable) {
			replyError(w, "builtin skill package download failed: "+err.Error(), http.StatusBadGateway)
			return
		}
		replyServiceError(w, err)
		return
	}
	common.ReplyOK(w, response)
}

func newDistributionService(db *gorm.DB) *skilldistribution.Service {
	return skilldistribution.NewService(skilldistribution.ServiceDeps{
		DB:       db,
		Blobs:    skillservice.NewBlobStore(db, skillservice.NewLocalObjectStore(skillObjectRoot())),
		Provider: skillbuiltin.DistributionProvider{},
	})
}
