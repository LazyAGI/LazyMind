package modelprovider

import (
	"reflect"
	"testing"
)

func TestModelReadyResponseCanExpressCloudEntitlementWithoutPlanDetails(t *testing.T) {
	typeOf := reflect.TypeOf(modelReadyResponse{})
	jsonFields := map[string]struct{}{}
	for index := 0; index < typeOf.NumField(); index++ {
		tag := typeOf.Field(index).Tag.Get("json")
		for end, char := range tag {
			if char == ',' {
				tag = tag[:end]
				break
			}
		}
		if tag != "" && tag != "-" {
			jsonFields[tag] = struct{}{}
		}
	}
	for _, required := range []string{"ready", "source", "reason", "cloud_plan_url"} {
		if _, found := jsonFields[required]; !found {
			t.Errorf("model readiness response omitted %q", required)
		}
	}
	for _, forbidden := range []string{"plan_id", "version", "periodic_quota", "usage", "remaining", "next_refresh_at"} {
		if _, found := jsonFields[forbidden]; found {
			t.Errorf("Desktop readiness response exposes Cloud-only Plan detail %q", forbidden)
		}
	}
}
