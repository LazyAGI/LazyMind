package controlstore

import (
	"encoding/json"
	"gorm.io/gorm"
	"lazymind/core/common/orm"
)

func ClearEditPause(tx *gorm.DB, session *orm.WorkflowSession) error {
	binding, err := DecodeBinding(*session)
	if err != nil {
		return err
	}
	if !binding.EditPaused {
		return nil
	}
	binding.EditPaused = false
	raw, err := json.Marshal(binding)
	if err != nil {
		return err
	}
	session.ControlBindingJSON = string(raw)
	return tx.Model(session).Update("control_binding_json", session.ControlBindingJSON).Error
}

func EditPaused(session orm.WorkflowSession) bool {
	binding, err := DecodeBinding(session)
	return err == nil && binding.EditPaused
}
