# 模型列表移出与能力图标控制设计

## 背景

模型配置界面当前同时暴露了“禁用/启用”和“删除”两套模型动作。已加入模型列表中的减号会写入 `enabled=false`，而垃圾桶才会调用现有删除接口。这会让减号的直觉语义与实际行为不一致，也让目录抽屉出现重复入口。

能力编辑器当前以“支持情况”下拉和“启用”开关两列展示视觉、工具调用、推理。需求是将这三项压缩成简洁的图标按钮，按下代表启用，并隐藏支持情况编辑逻辑，同时保持已有配置和供应商 key 的前向兼容。

## 目标

1. 已加入模型列表和模型目录抽屉中的减号统一表示移出模型。
2. 移除重复的垃圾桶入口。
3. 用三个可访问的图标按钮表示视觉、工具调用、推理的启用状态。
4. 不丢失已有供应商 API key、模型 ID、embedding 配置或旧模型设置字段。
5. 保持后端现有 `feature_support` / `feature_enabled` 数据结构可读，避免旧配置行为突然变化。

## 非目标

- 不删除后端的 `enabled`、`feature_support` 字段和接口。
- 不迁移或重置供应商分组的 API key。
- 不改变 embedding 删除的索引降级确认流程。
- 不新增模型配置字段或改变模型选择接口。

## 设计

### 1. 模型移出动作

`SavedProviderModels` 的模型操作区只保留编辑、探测和减号。减号直接调用现有 `onRemove` 回调，对应后端模型删除接口。原有单独的 `DeleteOutlined` 按钮移除。

`ProviderModelCatalogDrawer` 的单模型、任务分组和全部移出按钮都调用 `onRemove`。目录中的现有模型不再根据 `enabled` 显示 Plus/启用分支，也不再显示第二个垃圾桶按钮。历史上已经保存为 `enabled=false` 的模型仍可通过减号移出，但不提供恢复启用入口。

后端删除是软删除，清理模型选择引用但不修改供应商分组的 API key；重新添加同名模型仍由现有恢复逻辑处理。embedding 模型删除继续沿用现有 409 确认和重试流程。

### 2. 能力图标编辑器

`ModelFeaturesEditor` 改为一行三个图标按钮：

- 视觉：眼睛图标
- 工具调用：工具图标
- 推理：灯泡/推理图标

按钮使用 `aria-pressed` 表示状态，支持键盘操作、焦点样式和 tooltip。按下状态只表示当前请求中启用该能力；不再渲染“支持情况”表头、下拉框或说明文本。

初始状态仍通过现有 `isModelFeatureEnabled` 规则计算：显式 `feature_support=false` 的旧配置初始为关闭，旧的 `feature_enabled` 优先级和 legacy vision fallback 保持不变。

### 3. 旧设置兼容与保存

未触碰任一能力图标时，保存流程继续保留已有 `feature_support` 和 `feature_enabled` 内容，保证只修改模型名称、上下文等字段时旧设置 round-trip 不变。

用户点击某个图标后，该能力写入显式 `feature_enabled`。如果旧数据对该能力保存了 `feature_support=false`，并且用户主动打开图标，则只移除该能力的旧“不支持”声明，再提交显式启用，避免触发后端“声明不支持却启用”的校验。其他能力及未触碰字段原样保留。新模型只写入用户实际设置的 `feature_enabled` 字段。

后端 `Normalize`、`Effective` 和 API key 存储逻辑不改，继续负责兼容旧客户端和旧配置。

## 组件与数据流

1. 页面把现有 `onRemove` 传给已加入列表和目录抽屉；删除成功后沿用当前本地列表刷新逻辑。
2. 列表/抽屉不再调用 `setGroupModelEnabled`。
3. 编辑器通过表单维护 `modelSettings.feature_enabled`，并记录能力是否被用户触碰；`editableModelSettings` 在未触碰时保留旧支持声明，在触碰时生成兼容的显式 enabled 数据。
4. 保存请求继续走现有模型创建/更新接口，不改变 API key 请求体。

## 错误处理

- 删除请求失败时保留当前行，显示现有错误提示和重试状态。
- embedding 删除继续展示索引降级确认对话框。
- 目录批量删除继续允许部分成功，并显示成功/失败数量。
- 后端拒绝旧设置时沿用现有保存错误展示；前端不得静默丢弃设置字段。

## 验证

- 更新 `SavedProviderModels`、`ProviderModelCatalogDrawer`、`ModelEditorModal` 及 `ModelProvidersPage` 的组件测试。
- 覆盖减号调用删除、旧 `enabled=false` 行的移出、批量移出、图标 `aria-pressed`、旧 `feature_support` round-trip，以及主动覆盖旧不支持声明的保存行为。
- 运行相关前端 Vitest 测试和 `backend/core` 下已有 model settings / model provider 兼容测试。
