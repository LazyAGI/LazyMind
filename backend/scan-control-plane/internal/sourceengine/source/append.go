package source

import (
	"context"
	"fmt"
	"time"

	"github.com/lazymind/scan_control_plane/internal/sourceengine/connector"
	store "github.com/lazymind/scan_control_plane/internal/store/source"
)

// AppendSource 在已有数据源上追加新的文档/文件夹绑定。
//
// 只处理新增的 bindings，保留原数据源的所有配置、已有绑定关系和同步状态。
// 新增 binding 会自动继承同连接器类型下已有 binding 的配置：
//   - 同步策略（sync_mode、schedule_policy）
//   - 权限凭证（agent_id、auth_connection_id）
//   - 文件过滤规则（include/exclude_extensions）
// 前端只需传 target_ref 和 display_name 即可。
func (e *DefaultEngine) AppendSource(ctx context.Context, req AppendSourceRequest) (AppendSourceResponse, error) {
	// 1. 校验基本请求结构
	if err := validateAppendRequest(req); err != nil {
		return AppendSourceResponse{}, err
	}

	// 2. 获取并验证数据源状态
	src, err := e.repo.GetSource(ctx, req.SourceID)
	if err != nil {
		return AppendSourceResponse{}, mapStoreError(err)
	}
	if src.Status != SourceStatusActive {
		return AppendSourceResponse{}, NewError(ErrCodeInvalidRequest, "source is not active")
	}

	// 3. 加载已有 binding，用于继承默认配置
	existingBindings, err := e.repo.ListBindings(ctx, req.SourceID)
	if err != nil {
		return AppendSourceResponse{}, mapStoreError(err)
	}

	// 4. 从同连接器类型的已有 binding 继承未填写的字段
	//    用户只需传 target_ref 和 display_name，其余自动补齐
	inputs := inheritBindingDefaults(req.Bindings, existingBindings)

	// 5. 校验补齐后的每个 binding input
	for i, input := range inputs {
		if err := validateBindingInput(input, true); err != nil {
			return AppendSourceResponse{}, fmt.Errorf("bindings[%d]: %w", i, err)
		}
	}

	// 6. 为新 binding 创建 core folder 并校验目标合法性
	now := e.clock().UTC()
	prepared, err := e.prepareAppendBindings(ctx, src, req.CallerID, req.TenantID, inputs, now)
	if err != nil {
		return AppendSourceResponse{}, err
	}

	// 7. 检查与数据源现有 binding 的 target 是否重复
	for _, item := range prepared {
		if err := e.ensureUniqueTarget(ctx, item.binding, ""); err != nil {
			e.cleanupAppendFolders(ctx, src.DatasetID, req.CallerID, prepared)
			return AppendSourceResponse{}, err
		}
	}

	// 8. 持久化新 binding 及 checkpoint
	for _, item := range prepared {
		if err := mapStoreError(e.repo.AddBinding(ctx, item.binding, item.checkpoint)); err != nil {
			e.cleanupAppendFolders(ctx, src.DatasetID, req.CallerID, prepared)
			return AppendSourceResponse{}, err
		}
	}

	// 9. 仅对新 binding 触发相应操作：
	//    - watch 模式：通知 agent 开始监听（skip_initial_scan=true，不拉全量）
	//    - manual/scheduled 模式：触发初始同步拉取文档
	newBindings := collectBindings(prepared)
	syncJobErrors := e.queueLocalWatcherStarts(ctx, src, newBindings)
	syncJobErrors = append(syncJobErrors, e.triggerInitialSyncsForAppend(ctx, newBindings)...)

	return AppendSourceResponse{
		NewBindingIDs: bindingIDs(prepared),
		NewBindings:   bindingsToResponse(newBindings),
		SyncJobErrors: syncJobErrors,
	}, nil
}

// validateAppendRequest 校验追加请求的基本结构。
func validateAppendRequest(req AppendSourceRequest) error {
	if req.SourceID == "" {
		return FieldError("source_id", "required")
	}
	if req.CallerID == "" {
		return FieldError("caller_id", "required")
	}
	if len(req.Bindings) == 0 {
		return FieldError("bindings", "at least one binding is required")
	}
	return nil
}

// inheritBindingDefaults 从已有 binding 中继承未填写的字段。
// 同连接器类型的第一个 active binding 作为模板。
// 用户只需传 target_ref 和 display_name，其余自动补齐。
func inheritBindingDefaults(inputs []BindingInput, existing []store.Binding) []BindingInput {
	// 按 connector_type 分组已有 active binding
	templates := make(map[string]store.Binding)
	for _, b := range existing {
		if b.Status != BindingStatusActive {
			continue
		}
		if _, ok := templates[b.ConnectorType]; !ok {
			templates[b.ConnectorType] = b
		}
	}

	// 如果数据源只有一种连接器类型，记录下来用于自动检测
	singleType := ""
	if len(templates) == 1 {
		for t := range templates {
			singleType = t
		}
	}

	out := make([]BindingInput, len(inputs))
	for i, input := range inputs {
		// 没传 connector_type 且数据源只有一种类型时自动检测
		connType := string(input.ConnectorType)
		if connType == "" && singleType != "" {
			connType = singleType
		}

		tpl, ok := templates[connType]
		if !ok {
			out[i] = input
			continue
		}
		// 补齐 connector_type 后再继承
		input.ConnectorType = connector.ConnectorType(connType)
		out[i] = inheritBinding(input, tpl)
	}
	return out
}

// inheritBinding 从模板 binding 继承单个 input 中未填写的字段。
func inheritBinding(input BindingInput, tpl store.Binding) BindingInput {
	if input.ConnectorType == "" {
		input.ConnectorType = connector.ConnectorType(tpl.ConnectorType)
	}
	if input.TargetType == "" {
		input.TargetType = connector.TargetType(tpl.TargetType)
	}
	if input.SyncMode == "" {
		input.SyncMode = tpl.SyncMode
		if tpl.SyncMode == SyncModeScheduled && len(tpl.SchedulePolicy) > 0 {
			input.SchedulePolicy = store.CloneJSON(tpl.SchedulePolicy)
		}
	}
	if input.AgentID == "" && tpl.AgentID != "" {
		input.AgentID = tpl.AgentID
	}
	if input.AuthConnectionID == "" && tpl.AuthConnectionID != "" {
		input.AuthConnectionID = tpl.AuthConnectionID
	}
	if len(input.IncludeExtensions) == 0 && len(tpl.IncludeExtensions) > 0 {
		input.IncludeExtensions = jsonStringSlice(tpl.IncludeExtensions, "items")
	}
	if len(input.ExcludeExtensions) == 0 && len(tpl.ExcludeExtensions) > 0 {
		input.ExcludeExtensions = jsonStringSlice(tpl.ExcludeExtensions, "items")
	}
	return input
}

// prepareAppendBindings 为追加请求准备所有新 binding。
// 复用 prepareCreateBinding，每个 binding 使用独立幂等键。
func (e *DefaultEngine) prepareAppendBindings(ctx context.Context, src store.Source, callerID, tenantID string, inputs []BindingInput, now time.Time) ([]preparedBinding, error) {
	prepared := make([]preparedBinding, 0, len(inputs))
	for index, input := range inputs {
		item, err := e.prepareCreateBinding(ctx, src.SourceID, src.DatasetID, src.Name, callerID, tenantID, "", index, input, now)
		if err != nil {
			e.cleanupAppendFolders(ctx, src.DatasetID, callerID, prepared)
			return prepared, err
		}
		prepared = append(prepared, item)
	}
	if fingerprint, duplicated := duplicateInRequest(prepared); duplicated {
		e.cleanupAppendFolders(ctx, src.DatasetID, callerID, prepared)
		return prepared, &EngineError{
			Code:    ErrCodeBindingTargetDuplicated,
			Message: "binding target is duplicated in request",
			Details: map[string]any{"target_fingerprint": fingerprint},
		}
	}
	return prepared, nil
}

// cleanupAppendFolders 补偿：按倒序删除已创建的 core folder。
func (e *DefaultEngine) cleanupAppendFolders(ctx context.Context, datasetID, callerID string, prepared []preparedBinding) {
	for i := len(prepared) - 1; i >= 0; i-- {
		_ = e.deleteCoreFolder(ctx, datasetID, prepared[i].binding.CoreParentDocumentID, callerID)
	}
}

// triggerInitialSyncsForAppend 只对新 binding 触发初始同步。
func (e *DefaultEngine) triggerInitialSyncsForAppend(ctx context.Context, bindings []store.Binding) []JobError {
	var errs []JobError
	for _, binding := range bindings {
		_, err := e.schedule.TriggerInitialSync(ctx, binding)
		if err != nil {
			jobErr := JobError{
				Code:    string(ErrCodeInternal),
				Message: err.Error(),
				Details: map[string]any{"binding_id": binding.BindingID},
			}
			if recordErr := e.recordSyncJobError(ctx, binding, jobErr); recordErr != nil {
				jobErr.Details["record_error"] = recordErr.Error()
			}
			errs = append(errs, jobErr)
		}
	}
	return errs
}

// bindingIDs 从 preparedBinding 切片中提取所有 BindingID。
func bindingIDs(prepared []preparedBinding) []string {
	ids := make([]string, 0, len(prepared))
	for _, item := range prepared {
		ids = append(ids, item.binding.BindingID)
	}
	return ids
}
