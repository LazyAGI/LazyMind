# 模型供应商连接分组

## 读取已保存配置

`GET /api/core/model_providers/{model_provider_id}/groups` 返回当前用户拥有的未删除连接分组。分组列表包含 `id`、`user_model_provider_id`、`name`、`base_url`、`has_api_key`、`model_count`、`keys` 和 `is_verified`，不返回原始 API Key 或密文。`keys` 中仅包含用于操作的 `id`、脱敏的 `masked` 和启用状态 `enabled`。

某个分组的凭据无法读取时，仍返回该分组的配置元数据，并附带 `credential_status: "unavailable"`、空的 `keys` 和 `is_verified: false`。`has_api_key` 表示已保存凭据的存在，不代表凭据可用。其他分组照常返回；读取操作不清空异常凭据，也不修改其持久化验证状态。

前端显示“密钥不可用”及恢复提示，允许通过新增密钥入口重新填写，使用户切换页面或重新加载后仍能看到已保存的配置。

## 重新填写密钥

`PATCH /api/core/model_providers/{model_provider_id}/groups/{group_id}` 可以使用非空 `api_key` 替换无法读取的旧凭据。模型供应商沿用正常的保存流程：先保存，再单独检查连接；新密钥恢复后不会继承不可读取旧密钥的验证状态。辅助服务仍先验证新密钥，验证失败时保留原配置。保存成功后按现有加密方式保存新凭据、推进凭据修订号，并清除前端的不可用提示。

旧凭据无法读取且未提供替换密钥时，返回 HTTP 400（`api_key is required`），不修改已保存配置。
