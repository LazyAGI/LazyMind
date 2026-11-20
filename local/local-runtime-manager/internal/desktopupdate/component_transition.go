package desktopupdate

import (
	"reflect"
	"slices"
)

func UpdateComponentIDs() []string { return []string{"rag", "cluster"} }

func IsUpdateComponent(id string) bool { return slices.Contains(UpdateComponentIDs(), id) }

func validUpdateComponent(meta ComponentDescriptor) bool {
	if !IsUpdateComponent(meta.ID) || !digestPattern.MatchString(meta.Revision) || !digestPattern.MatchString(meta.SHA256) ||
		!digestPattern.MatchString(meta.Tag) || !digestPattern.MatchString(meta.BaseFingerprint) || meta.PythonABI == "" || len(meta.Packages) == 0 || len(meta.Packages) > 256 {
		return false
	}
	for name, version := range meta.Packages {
		if name == "" || version == "" {
			return false
		}
	}
	return true
}

func validateComponentTransitions(transitions map[string]ComponentTransition, targetSHA string) error {
	if len(transitions) != 1 {
		return failure("component_contract_invalid", nil)
	}
	rag, ok := transitions["rag"]
	if !ok || rag.TargetComponentsSHA256 != targetSHA || len(rag.SourceClasses) == 0 || len(rag.SourceClasses) > 128 {
		return failure("component_contract_invalid", nil)
	}
	for _, c := range rag.SourceClasses {
		if !digestPattern.MatchString(c.Tag) || !digestPattern.MatchString(c.BaseFingerprint) || c.PythonABI == "" || len(c.Packages) == 0 || len(c.Packages) > 256 {
			return failure("component_contract_invalid", nil)
		}
		for name, version := range c.Packages {
			if name == "" || version == "" {
				return failure("component_contract_invalid", nil)
			}
		}
	}
	return nil
}

func matchSourceComponents(transitions map[string]ComponentTransition, state LocalState, c Config) string {
	if len(state.Components) != len(state.ComponentMetadata) {
		return "component_state_unknown"
	}
	for id, revision := range state.Components {
		meta, ok := state.ComponentMetadata[id]
		if !ok || meta.ID != id || meta.Revision != revision || !validUpdateComponent(meta) || meta.Platform != c.Platform || meta.Arch != c.Arch {
			return "component_state_unknown"
		}
	}
	meta, ok := state.ComponentMetadata["rag"]
	if !ok {
		return ""
	}
	for _, accepted := range transitions["rag"].SourceClasses {
		if accepted.Tag == meta.Tag && accepted.PythonABI == meta.PythonABI && accepted.BaseFingerprint == meta.BaseFingerprint && reflect.DeepEqual(accepted.Packages, meta.Packages) {
			return ""
		}
	}
	return "component_update_incompatible"
}
