package common

import (
	"net/http"
	"strings"
)

func init() {
	for _, source := range []string{
		"expected exactly one json object",
		"skill installation requires an editor task",
		"provide exactly one of url or package_zip_base64",
		"skill.md frontmatter name must match expected name %q",
		"clawhub url must not contain credentials or a port",
		"clawhub url must point to /<owner>/skills/<slug> or /<owner>/<slug>",
		"github url must not contain credentials or a port",
		"github raw url must point to skill.md",
		"github url must point to a repository, /tree/<ref>/<skill-path>, or skill.md",
		"skill import url must not contain credentials",
		"skillhub url must not contain credentials or a port",
		"skills.sh url must use http(s) without credentials or a port",
		"skills.sh url must identify /<owner>/<repository>/<skill>",
		"skills.sh skill name is ambiguous; use an exact github skill directory url",
		"skillsmp url must use https without credentials or a port",
		"skillsmp url contains an invalid path segment",
		"skillsmp url must identify a skill at /skills/<id> or /creators/<owner>/<repo>/<skill>",
		"skillsmp page declares an invalid github source",
		"skillsmp page must declare an exact github skill directory or skill.md",
		"skillsmp page declares an invalid github skill source",
		"skillsmp page declares multiple different skill sources",
		"skillsmp page did not declare an exact github skill source",
		"github repository contains multiple skills; use a specific skill directory or skill.md url",
	} {
		if strings.Contains(source, "%") {
			registerAdditionalErrorPattern(source, "Invalid request", http.StatusBadRequest, 2000103)
			continue
		}
		registerAdditionalErrorAlias(source, "Invalid request", http.StatusBadRequest, 2000103)
	}
	for _, source := range []string{
		"skill package exceeds the 100 mib package limit",
		"skill package request exceeds the 100 mib package limit",
		"skill package archive exceeds %d bytes",
		"skill package download exceeds transfer limit",
		"clawhub response is neither a zip nor a handoff within the 1 mib limit",
		"github api response exceeds 8 mib",
		"input_files exceed the %d mib total limit",
		"input_files must contain at most %d files",
		"input_files[%d] exceeds the %d mib file limit",
		"input_bindings must contain at most %d materials",
		"input_bindings must contain at most %d resources",
		"skills.sh discovery exceeds its byte limit",
		"skills.sh skill frontmatter exceeds %d bytes",
		"skills.sh source document exceeds %d bytes",
		"skill frontmatter exceeds %d bytes",
		"archive discovery exceeds %d skill documents",
		"archive frontmatter discovery exceeds %d bytes",
	} {
		if strings.Contains(source, "%") {
			registerAdditionalErrorPattern(source, "Invalid skill package", http.StatusRequestEntityTooLarge, 2003300)
			continue
		}
		registerAdditionalErrorAlias(source, "Invalid skill package", http.StatusRequestEntityTooLarge, 2003300)
	}
	for _, source := range []string{
		"clawhub handoff could not be read",
		"clawhub github package could not be selected",
		"clawhub github folder content hash does not match the approved source",
		"github archive does not have one repository wrapper",
		"clawhub response must be a zip or a public-github source handoff",
		"clawhub github handoff requires a valid repository, pinned 40-character commit, and skill directory",
		"clawhub github handoff is missing a valid content hash",
		"clawhub github handoff has an invalid content hash",
		"github api returned invalid json",
		"stopped after 10 redirects",
		"github api request failed",
		"github api response could not be read",
		"github api rate limit reached; retry after %s (retry-after)",
		"github api rate limit reached; retry after %s",
		"github api rate limit reached; retry in at least one minute, or configure lazymind_github_token for authenticated requests",
		"github reference discovery failed with http status %d",
		"github reference advertisement is incomplete",
		"github reference advertisement has an invalid packet",
		"github url ref was not advertised",
		"github head could not be discovered",
		"%s skill has an invalid github source url",
		"%s skill must resolve to a github repository directory",
		"%s url contains an invalid path segment",
		"%s url contains an invalid query",
		"%s url contains an invalid %s selector",
		"%s url must select either a version or a tag",
		"%w; github archive discovery failed",
		"http status %d",
		"archive contains a symlink skill document",
		"archive contains duplicate path %q",
		"archive must have one repository root",
		"unsafe archive path %q",
		"skills.sh github source could not be resolved",
		"skills.sh github source returned an invalid commit",
		"skills.sh repository tree could not be read",
		"skills.sh repository tree is truncated",
		"skills.sh repository contains an unsafe skill path",
		"skills.sh skill discovery exceeds %d http documents",
		"skills.sh skill discovery failed",
		"skills.sh skill %q was not found in %s/%s",
		"skills.sh source fetch failed with http status %d",
		"skills.sh source redirect is not trusted",
		"skills.sh repository tree lookup failed with http status %d",
		"skillsmp page redirected too many times",
		"skillsmp page request could not be created",
		"skillsmp page could not be fetched",
		"skillsmp page returned http status %d",
		"skillsmp page could not be read",
		"skillsmp page exceeds the size limit",
		"skillsmp page html could not be read",
	} {
		if strings.Contains(source, "%") {
			registerAdditionalErrorPattern(source, "Skill import failed", http.StatusBadGateway, 2003301)
			continue
		}
		registerAdditionalErrorAlias(source, "Skill import failed", http.StatusBadGateway, 2003301)
	}
}
