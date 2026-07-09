package preference

import (
	"lazymind/core/common/orm"
	"lazymind/core/preferencefile"
)

type PreferenceFile = preferencefile.PreferenceFile
type PreferencePatch = preferencefile.PreferencePatch

func BuildInitialFileContent(row orm.SystemUserPreference) string {
	return preferencefile.BuildInitialFileContent(row)
}

func EmptyPreferenceFileContent() string {
	return preferencefile.EmptyPreferenceFileContent()
}

func ParseFileContent(content string) (PreferenceFile, error) {
	return preferencefile.ParseFileContent(content)
}

func PatchFileContent(content string, patch PreferencePatch) (string, PreferenceFile, error) {
	return preferencefile.PatchFileContent(content, patch)
}
