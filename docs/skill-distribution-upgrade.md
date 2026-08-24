# Skill 分发升级与三方合并

内置 Skill 安装时，Core 会把最终分发包保存为不可变的 Distribution Artifact，并将用户 Skill 绑定到该制品。后续平台发布新分发包时，升级流程使用三棵文件树：

```text
Base   = 当前绑定的旧分发包 D0
Ours   = 用户当前 Head Revision
Theirs = 最新分发包 D1
```

文件树首先按路径处理新增、删除、二进制和类型冲突；双方都修改的文本文件使用进程内、逐行三方合并。无冲突修改自动合并；冲突区域在候选中选择平台侧内容，同时保存结构化冲突，不写入 `<<<<<<<` 等冲突标记。

## API

查询升级状态：

```http
GET /api/core/skills/{skill_id}/distribution-upgrade
```

准备升级 Draft：

```http
POST /api/core/skills/{skill_id}/distribution-upgrade:prepare
```

准备成功后，候选内容进入现有 `skill_draft_entries`，后续继续使用已有的 Skill Diff、hunk Review、Commit 和 Rollback API。存在结构化冲突时，直接 Commit 会被拒绝，必须完成 Draft Review。

## 自动更新与原有自动进化

分发更新复用 Skill 已有的 `auto_evo`（界面中的“自动更新”）开关，不新增第二套用户配置：

| 自动更新 | 合并结果 | 行为 |
| --- | --- | --- |
| 关闭 | 任意 | 只提示有平台新版，由用户手动准备并确认 |
| 开启 | 无冲突 | 后台准备并提交，创建一个平台更新 Revision |
| 开启 | 有冲突 | 后台只准备候选，停在待确认状态，不创建 Revision |

平台分发更新与根据用户习惯产生的自动进化仍是两个修改来源，但共用现有 Draft、Review、Commit 和版本历史。分发升级 Draft 使用 `distribution_upgrade:<archive_sha256>` 标识，通用自动进化提交器会跳过它，避免冲突候选绕过用户确认。

## 状态变化

```text
up_to_date
  → prepare
pending_review
  → review/commit → up_to_date（Binding 切换到 D1）
  → discard       → up_to_date（继续绑定 D0）
```

进入 `pending_review` 时会固定本次候选的分发包 SHA 和版本。即使平台随后又发布 D2，当前确认页仍比较 D0/用户 Head/D1；用户完成或放弃 D1 后，下一次扫描才会处理 D2。重复调用 Prepare 返回同一个 Draft，不会重复创建版本。

提交升级会创建 `change_source=distribution_upgrade` 的新 Revision，并记录目标分发包 SHA。回滚到旧 Revision 时，系统沿 Revision 祖先查找对应的分发制品并恢复 Binding。

历史安装如果没有 Distribution Binding，状态查询会尝试从最早的 `builtin_package` Revision 来源信息恢复 D0；如果初始 Revision 已不存在或来源信息无法解析，则返回“分发基线不可用”，不会退化为不安全的双向覆盖。
