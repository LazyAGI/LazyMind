package doc

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"os"
	"strings"

	"lazymind/core/common"
	"lazymind/core/store"
)

func datasetCatalogCallerFromRequest(r *http.Request) DatasetCatalogCaller {
	caller, _ := resolveDatasetCatalogCallerFromRequest(r)
	return caller
}

func resolveDatasetCatalogCallerFromRequest(r *http.Request) (DatasetCatalogCaller, error) {
	caller := DatasetCatalogCaller{}
	if r == nil {
		return caller, errors.New("account identity unavailable or disabled")
	}
	caller.Authorization = strings.TrimSpace(r.Header.Get("Authorization"))
	caller.UserID = strings.TrimSpace(r.Header.Get("X-User-ID"))
	if caller.UserID == "" {
		if userID := strings.TrimSpace(store.UserID(r)); userID != "" {
			caller.UserID = userID
		}
	}
	caller.TenantID = strings.TrimSpace(r.Header.Get("X-Tenant-ID"))
	caller.UserRole = strings.TrimSpace(r.Header.Get("X-User-Role"))
	if provided := strings.TrimSpace(r.Header.Get("X-LazyMind-Internal-Token")); provided != "" {
		expected := strings.TrimSpace(os.Getenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN"))
		if expected == "" || subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) != 1 {
			return DatasetCatalogCaller{}, errors.New("account identity unavailable or disabled")
		}
		identity, err := common.GetCurrentIdentity(r.Context(), caller.UserID)
		if err != nil {
			return DatasetCatalogCaller{}, err
		}
		caller.TenantID, caller.UserRole = identity.TenantID, identity.Role
		caller.Permissions = identity.Permissions
	}
	return caller, nil
}

func uniqueNonEmptyStrings(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
