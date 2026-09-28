package common

import "net/http"

func init() {
	registerAdditionalErrorAlias("environment input expired or unavailable; request a new input card", "Environment input expired or unavailable; request a new input card", http.StatusConflict, 2003125)
	registerAdditionalErrorAlias("check user env var failed", "Failed to check environment variables", http.StatusInternalServerError, 2003130)
	registerAdditionalErrorAlias("env name already exists", "Environment variable name already exists", http.StatusConflict, 2003131)
	registerAdditionalErrorAlias("create user env var failed", "Failed to create environment variable", http.StatusInternalServerError, 2003132)
	registerAdditionalErrorAlias("missing env id", "Environment variable ID is required", http.StatusBadRequest, 2003133)
	registerAdditionalErrorAlias("env var not found", "Environment variable not found", http.StatusNotFound, 2003134)
	registerAdditionalErrorAlias("environment variable description is too long", "Environment variable description is too long", http.StatusBadRequest, 2003135)
	registerAdditionalErrorAlias("delete user env var failed", "Failed to delete environment variable", http.StatusInternalServerError, 2003116)
	for _, source := range []string{
		"environment variable deletion confirmation is invalid or already answered",
		"environment variable deletion confirmation is invalid",
	} {
		registerAdditionalErrorAlias(source, "Environment variable deletion confirmation is invalid or already answered", http.StatusConflict, 2003117)
	}
	registerAdditionalErrorAlias("environment variable deletion requires an explicit confirmation answer", "Confirm whether to delete the environment variable", http.StatusConflict, 2003118)
	for _, source := range []string{
		"environment variable changed or is unavailable; request a new deletion confirmation",
		"environment variable changed; reload and retry",
	} {
		registerAdditionalErrorAlias(source, "Environment variable changed or is unavailable; reload and retry", http.StatusConflict, 2003119)
	}
	registerAdditionalErrorAlias("env name is required", "Environment variable name is required", http.StatusBadRequest, 2003120)
	registerAdditionalErrorAlias("env name must match ^[A-Za-z_][A-Za-z0-9_]*$", "Use up to 128 letters, digits or underscores; start with a letter or underscore", http.StatusBadRequest, 2003121)
	for _, source := range []string{"env name %q is reserved", "env name %q controls runtime behavior"} {
		registerAdditionalErrorPattern(source, "Environment variable name is reserved for runtime control", http.StatusBadRequest, 2003122)
	}
	registerAdditionalErrorAlias("env name must look like a credential name, such as *_API_KEY, *_TOKEN, or *_SECRET", "Use a credential variable name such as *_API_KEY, *_TOKEN or *_SECRET", http.StatusBadRequest, 2003123)
	for _, source := range []string{
		"user environment configuration unavailable",
		"user env key file must be a private regular file",
		"cannot read user env key file",
		"user env server key must contain at least 32 bytes",
		"unsupported user env credential version",
		"cannot decrypt user env credential",
		"cannot decrypt legacy user env credential",
	} {
		registerAdditionalErrorAlias(source, "Unable to access user environment variables; check the credential key configuration", http.StatusInternalServerError, 2003124)
	}
}
