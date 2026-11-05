package desktopupdate

import "reflect"

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
	if len(state.Components) == 0 {
		if len(state.ComponentMetadata) != 0 {
			return "component_state_unknown"
		}
		return ""
	}
	if len(state.Components) != 1 || len(state.ComponentMetadata) != 1 {
		return "component_state_unknown"
	}
	revision := state.Components["rag"]
	meta, ok := state.ComponentMetadata["rag"]
	if !ok || meta.ID != "rag" || meta.Revision != revision || !digestPattern.MatchString(revision) || !digestPattern.MatchString(meta.SHA256) || meta.Platform != c.Platform || meta.Arch != c.Arch {
		return "component_state_unknown"
	}
	for _, accepted := range transitions["rag"].SourceClasses {
		if accepted.Tag == meta.Tag && accepted.PythonABI == meta.PythonABI && accepted.BaseFingerprint == meta.BaseFingerprint && reflect.DeepEqual(accepted.Packages, meta.Packages) {
			return ""
		}
	}
	return "component_update_incompatible"
}
