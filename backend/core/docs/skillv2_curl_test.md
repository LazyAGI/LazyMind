# Skill v2 curl 测试文档

服务地址固定为 `http://10.210.0.49:5024/api/core`。本文档不使用 shell 变量；命令中的 `REPLACE_WITH_SKILL_ID`、`REPLACE_WITH_DRAFT_VERSION`、`REPLACE_WITH_REVISION_ID`、`REPLACE_WITH_REVIEW_ID`、`REPLACE_WITH_REVIEW_VERSION`、`REPLACE_WITH_HUNK_ID`、`REPLACE_WITH_MARKET_ITEM_ID`、`REPLACE_WITH_SHARE_ITEM_ID` 是需要你手动替换的占位文本，不是变量。

建议先执行“启用内置技能”，从响应 `data.skill_id` 拿到真实 Skill ID，再把后续命令 URL 中的 `REPLACE_WITH_SKILL_ID` 手动替换成这个值。

## 已实现功能

- 技能目录型模型：`skills` 只保存根元数据，文件内容进入 `skill_blobs`，正式版本进入 `skill_revisions` 和 `skill_revision_entries`，草稿进入 `skill_drafts` 和 `skill_draft_entries`。
- 技能 CRUD：列表、详情、创建、编辑、删除、标签、分类、关键词搜索。
- 内置技能启用：按 builtin uid 复制内置目录包到用户自己的 v2 Skill。
- 文件系统能力：目录树、文件读取、一级列表、路径信息、路径存在判断、下载入口。
- 草稿 overlay：文本写入、上传文件写入、创建目录、删除路径、移动路径、草稿存在和草稿状态。
- 版本管理：提交草稿、revision 列表、revision 详情、revision tree、revision file、回滚预览、回滚、删除 revision。
- Diff：tree diff、file diff，返回 GitHub-like `diff_entry_lines`，draft review 场景会在响应顶层返回 `review_id`、`review_version`，并在 `type=HUNK` 的 diff line 上返回 `hunk_id` 和 `decision`。
- RemoteFS：`/remote-fs/list/info/exists/content/path/move`，支持 `task_id` 聚合同一次编辑任务。
- 技能市场：市场列表、详情、管理员上架/编辑/下架、用户安装，安装是复制目录树。
- 分享：创建分享、查询发出和收到的分享、分享详情、接受分享、拒绝分享，接受是复制目录树。
- 自动演进/Review 对接：生成写入草稿，确认提交为新 revision，审批接受创建新 revision。
- 搜索索引：`skill_search_indexes` 从 head revision 文本文件和技能元数据重建，搜索不作为目录树、diff、回滚真源。
- 统一语义错误码：错误响应沿用现有 envelope，并在 `data.code` 返回 `invalid_path`、`draft_conflict`、`empty_draft` 等语义码。

## 主要目录

- `backend/core/skillv2/service`：Skill CRUD、导入、BlobStore、Review 接受、自动演进草稿写入。
- `backend/core/skillv2/fs`：HeadFS、DraftFS、草稿状态、草稿写入和提交兼容服务。
- `backend/core/skillv2/revision`：commit、revision query、rollback、revision delete、blob 引用清理。
- `backend/core/skillv2/diff`：tree/file diff、uploaded ref 解析、GitHub-like diff lines。
- `backend/core/skillv2/remotefs`：`/remote-fs/*` 文件系统接口。
- `backend/core/skillv2/market`：市场上架、安装、复制目录树。
- `backend/core/skillv2/share`：分享接受、复制目录树。
- `backend/core/skillv2/search`：搜索索引重建、查询和缺表降级。
- `backend/core/skillv2/httperr`：v2 语义错误码映射和响应。
- `backend/core/skillv2/handler`：HTTP handler，连接路由和 v2 service。
- `backend/core/resourceupdate/skill_v2_bridge.go`：审批/Review 到 v2 的桥接。
- `backend/core/evolution/service.go`：自动演进读取 v2 head/draft，旧模型仅 fallback。
- `backend/core/migrations/20260704180000_create_skill_v2_tables.up.sql`：v2 新表 migration。
- `backend/core/common/orm/skill_v2_models.go`：v2 ORM models。

## 通用 Header

大部分接口通过 `X-User-Id` 识别用户。下面命令使用两个固定测试用户：

- 发起方：`user_001` / `张三`
- 接收方：`user_002` / `李四`

经过网关访问 `10.210.0.49:5024` 时需要携带登录态：

```bash
-H 'Authorization: Bearer REPLACE_WITH_TOKEN'
```

## 1. 启用内置技能并拿到 Skill ID

```bash
curl -sS -X POST 'http://10.210.0.49:5024/api/core/builtin-skills/bsk_01JZ7Q3YF6Q2Z4HM9V8K7D1R3P:enable' \
  -H 'X-User-Id: user_001' \
  -H 'X-User-Name: 张三' \
  -H 'Content-Type: application/json'
```

从响应中记录 `data.skill_id`。后续命令把 `REPLACE_WITH_SKILL_ID` 手动替换为该值。

## 2. 技能列表、搜索、标签、分类

也可以通过 `POST /skills` 显式新建技能。新建依赖已上传 ZIP 或可访问的 ZIP URL；ZIP 包必须包含 `SKILL.md`。如果使用上传方式，把 `REPLACE_WITH_UPLOAD_ID` 手动替换为已完成的 ZIP 上传 ID。

```bash
curl -sS -X POST 'http://10.210.0.49:5024/api/core/skills' \
  -H 'X-User-Id: user_001' \
  -H 'X-User-Name: 张三' \
  -H 'Content-Type: application/json' \
  -d '{"name":"curl-created-skill","category":"curl","description":"curl explicit create smoke test","tags":["curl","skillv2"],"auto_evo":false,"is_enabled":true,"source":{"type":"uploaded_zip","upload_id":"REPLACE_WITH_UPLOAD_ID"}}'
```

如果使用 URL 导入，把 `https://example.com/skill.zip` 手动替换为一个服务可访问的真实 ZIP 地址。

```bash
curl -sS -X POST 'http://10.210.0.49:5024/api/core/skills' \
  -H 'X-User-Id: user_001' \
  -H 'X-User-Name: 张三' \
  -H 'Content-Type: application/json' \
  -d '{"name":"curl-url-skill","category":"curl","description":"curl url create smoke test","tags":["curl","url"],"auto_evo":false,"is_enabled":true,"source":{"type":"url","url":"https://example.com/skill.zip"}}'
```

编辑技能元数据示例。

```bash
curl -sS -X PATCH 'http://10.210.0.49:5024/api/core/skills/REPLACE_WITH_SKILL_ID' \
  -H 'X-User-Id: user_001' \
  -H 'X-User-Name: 张三' \
  -H 'Content-Type: application/json' \
  -d '{"description":"updated by curl smoke test","tags":["curl","updated"],"auto_evo":false,"is_enabled":true}'
```

删除技能示例。这个操作会删除该用户自己的 v2 Skill 图谱，谨慎在测试数据上执行。

```bash
curl -sS -X DELETE 'http://10.210.0.49:5024/api/core/skills/REPLACE_WITH_SKILL_ID' \
  -H 'X-User-Id: user_001' \
  -H 'X-User-Name: 张三'
```

```bash
curl -sS 'http://10.210.0.49:5024/api/core/skills?page=1&page_size=20' \
  -H 'X-User-Id: user_001' \
  -H 'X-User-Name: 张三'
```

```bash
curl -sS 'http://10.210.0.49:5024/api/core/skills?keyword=research&page=1&page_size=20' \
  -H 'X-User-Id: user_001' \
  -H 'X-User-Name: 张三'
```

```bash
curl -sS 'http://10.210.0.49:5024/api/core/skills/tags' \
  -H 'X-User-Id: user_001' \
  -H 'X-User-Name: 张三'
```

```bash
curl -sS 'http://10.210.0.49:5024/api/core/skills/categories' \
  -H 'X-User-Id: user_001' \
  -H 'X-User-Name: 张三'
```

## 3. 技能详情、目录树、文件读取

```bash
curl -sS 'http://10.210.0.49:5024/api/core/skills/REPLACE_WITH_SKILL_ID' \
  -H 'X-User-Id: user_001' \
  -H 'X-User-Name: 张三'
```

```bash
curl -sS 'http://10.210.0.49:5024/api/core/skills/REPLACE_WITH_SKILL_ID/tree' \
  -H 'X-User-Id: user_001' \
  -H 'X-User-Name: 张三'
```

```bash
curl -sS 'http://10.210.0.49:5024/api/core/skills/REPLACE_WITH_SKILL_ID/file?path=SKILL.md' \
  -H 'X-User-Id: user_001' \
  -H 'X-User-Name: 张三'
```

```bash
curl -sS 'http://10.210.0.49:5024/api/core/skills/REPLACE_WITH_SKILL_ID/fs/list?path=' \
  -H 'X-User-Id: user_001' \
  -H 'X-User-Name: 张三'
```

```bash
curl -sS 'http://10.210.0.49:5024/api/core/skills/REPLACE_WITH_SKILL_ID/fs/info?path=SKILL.md' \
  -H 'X-User-Id: user_001' \
  -H 'X-User-Name: 张三'
```

```bash
curl -sS 'http://10.210.0.49:5024/api/core/skills/REPLACE_WITH_SKILL_ID/fs/exists?path=SKILL.md' \
  -H 'X-User-Id: user_001' \
  -H 'X-User-Name: 张三'
```

```bash
curl -sS 'http://10.210.0.49:5024/api/core/skills/REPLACE_WITH_SKILL_ID/fs/content?path=SKILL.md' \
  -H 'X-User-Id: user_001' \
  -H 'X-User-Name: 张三'
```

## 4. 草稿状态和草稿写入

先查询草稿状态，从响应中记录 `data.draft_version`。

```bash
curl -sS 'http://10.210.0.49:5024/api/core/skills/REPLACE_WITH_SKILL_ID/draft/status' \
  -H 'X-User-Id: user_001' \
  -H 'X-User-Name: 张三'
```

```bash
curl -sS 'http://10.210.0.49:5024/api/core/skills/REPLACE_WITH_SKILL_ID/draft/exists' \
  -H 'X-User-Id: user_001' \
  -H 'X-User-Name: 张三'
```

把下面请求体中的 `REPLACE_WITH_DRAFT_VERSION` 手动替换为上一步返回的 `draft_version`。

```bash
curl -sS -X PUT 'http://10.210.0.49:5024/api/core/skills/REPLACE_WITH_SKILL_ID/draft/fs/text' \
  -H 'X-User-Id: user_001' \
  -H 'X-User-Name: 张三' \
  -H 'Content-Type: application/json' \
  -d '{"path":"references/curl-check.md","content":"# curl check\n\nThis file was written by a curl smoke test.\n","expected_draft_version":REPLACE_WITH_DRAFT_VERSION}'
```

写入成功后响应会返回新的 `data.draft_version`。如果继续创建目录或移动路径，需要手动替换为最新 draft version。

```bash
curl -sS -X POST 'http://10.210.0.49:5024/api/core/skills/REPLACE_WITH_SKILL_ID/draft/fs/dir' \
  -H 'X-User-Id: user_001' \
  -H 'X-User-Name: 张三' \
  -H 'Content-Type: application/json' \
  -d '{"path":"notes","expected_draft_version":REPLACE_WITH_DRAFT_VERSION}'
```

```bash
curl -sS -X POST 'http://10.210.0.49:5024/api/core/skills/REPLACE_WITH_SKILL_ID/draft/fs/move' \
  -H 'X-User-Id: user_001' \
  -H 'X-User-Name: 张三' \
  -H 'Content-Type: application/json' \
  -d '{"from":"references/curl-check.md","to":"references/curl-check-renamed.md","expected_draft_version":REPLACE_WITH_DRAFT_VERSION}'
```

```bash
curl -sS -X DELETE 'http://10.210.0.49:5024/api/core/skills/REPLACE_WITH_SKILL_ID/draft/fs/path' \
  -H 'X-User-Id: user_001' \
  -H 'X-User-Name: 张三' \
  -H 'Content-Type: application/json' \
  -d '{"path":"notes","recursive":true,"expected_draft_version":REPLACE_WITH_DRAFT_VERSION}'
```

## 5. 提交草稿并查看 revision

提交时把 `REPLACE_WITH_DRAFT_VERSION` 手动替换为最新 draft version。

```bash
curl -sS -X POST 'http://10.210.0.49:5024/api/core/skills/REPLACE_WITH_SKILL_ID/commit' \
  -H 'X-User-Id: user_001' \
  -H 'X-User-Name: 张三' \
  -H 'Content-Type: application/json' \
  -d '{"draft_version":REPLACE_WITH_DRAFT_VERSION}'
```

从响应中记录 `data.revision_id`。

```bash
curl -sS 'http://10.210.0.49:5024/api/core/skills/REPLACE_WITH_SKILL_ID/revisions' \
  -H 'X-User-Id: user_001' \
  -H 'X-User-Name: 张三'
```

```bash
curl -sS 'http://10.210.0.49:5024/api/core/skills/REPLACE_WITH_SKILL_ID/revisions/REPLACE_WITH_REVISION_ID' \
  -H 'X-User-Id: user_001' \
  -H 'X-User-Name: 张三'
```

```bash
curl -sS 'http://10.210.0.49:5024/api/core/skills/REPLACE_WITH_SKILL_ID/revisions/REPLACE_WITH_REVISION_ID/tree' \
  -H 'X-User-Id: user_001' \
  -H 'X-User-Name: 张三'
```

```bash
curl -sS 'http://10.210.0.49:5024/api/core/skills/REPLACE_WITH_SKILL_ID/revisions/REPLACE_WITH_REVISION_ID/file?path=SKILL.md' \
  -H 'X-User-Id: user_001' \
  -H 'X-User-Name: 张三'
```

## 6. Diff

下面示例比较同一个 Skill 的 head 和 draft。没有草稿时可能没有差异。

```bash
curl -sS -X POST 'http://10.210.0.49:5024/api/core/skill-diff/tree' \
  -H 'X-User-Id: user_001' \
  -H 'X-User-Name: 张三' \
  -H 'Content-Type: application/json' \
  -d '{"old":{"type":"head","skill_id":"REPLACE_WITH_SKILL_ID"},"new":{"type":"draft","skill_id":"REPLACE_WITH_SKILL_ID"}}'
```

```bash
curl -sS -X POST 'http://10.210.0.49:5024/api/core/skill-diff/file' \
  -H 'X-User-Id: user_001' \
  -H 'X-User-Name: 张三' \
  -H 'Content-Type: application/json' \
  -d '{"old":{"type":"head","skill_id":"REPLACE_WITH_SKILL_ID"},"new":{"type":"draft","skill_id":"REPLACE_WITH_SKILL_ID"},"path":"SKILL.md","context_lines":3}'
```

从响应中记录：

- `data.review_id`
- `data.review_version`
- `data.diff_entry_lines[]` 中 `type=HUNK` 元素的 `hunk_id`

### 6.1 Draft Review 分块接受、拒绝、撤销和提交

接受或拒绝 diff block。`items` 支持一次提交多个 hunk。

```bash
curl -sS -X POST 'http://10.210.0.49:5024/api/core/skills/REPLACE_WITH_SKILL_ID/draft-review/REPLACE_WITH_REVIEW_ID/actions' \
  -H 'X-User-Id: user_001' \
  -H 'X-User-Name: 张三' \
  -H 'Content-Type: application/json' \
  -d '{"expected_review_version":REPLACE_WITH_REVIEW_VERSION,"items":[{"path":"SKILL.md","hunk_id":"REPLACE_WITH_HUNK_ID","decision":"accepted"}]}'
```

拒绝 diff block 时把 `decision` 改为 `rejected`。

```bash
curl -sS -X POST 'http://10.210.0.49:5024/api/core/skills/REPLACE_WITH_SKILL_ID/draft-review/REPLACE_WITH_REVIEW_ID/actions' \
  -H 'X-User-Id: user_001' \
  -H 'X-User-Name: 张三' \
  -H 'Content-Type: application/json' \
  -d '{"expected_review_version":REPLACE_WITH_REVIEW_VERSION,"items":[{"path":"SKILL.md","hunk_id":"REPLACE_WITH_HUNK_ID","decision":"rejected"}]}'
```

撤销上一步 review action。响应里的 `items` 表示被撤销的 hunk 回到的状态。

```bash
curl -sS -X POST 'http://10.210.0.49:5024/api/core/skills/REPLACE_WITH_SKILL_ID/draft-review/REPLACE_WITH_REVIEW_ID:undo' \
  -H 'X-User-Id: user_001' \
  -H 'X-User-Name: 张三' \
  -H 'Content-Type: application/json' \
  -d '{"expected_review_version":REPLACE_WITH_REVIEW_VERSION}'
```

提交 review 结果。提交成功会生成正式 revision，清空 draft overlay，并清理当前 `review_id` 对应的 review action 和 undo 数据。

```bash
curl -sS -X POST 'http://10.210.0.49:5024/api/core/skills/REPLACE_WITH_SKILL_ID/draft-review/REPLACE_WITH_REVIEW_ID:commit' \
  -H 'X-User-Id: user_001' \
  -H 'X-User-Name: 张三' \
  -H 'Content-Type: application/json' \
  -d '{"expected_review_version":REPLACE_WITH_REVIEW_VERSION}'
```

## 7. 回滚预览和回滚

把 `REPLACE_WITH_REVISION_ID` 手动替换为要回滚到的 revision ID。回滚会创建新的 head revision。

```bash
curl -sS -X POST 'http://10.210.0.49:5024/api/core/skills/REPLACE_WITH_SKILL_ID/rollback/preview' \
  -H 'X-User-Id: user_001' \
  -H 'X-User-Name: 张三' \
  -H 'Content-Type: application/json' \
  -d '{"target_revision_id":"REPLACE_WITH_REVISION_ID"}'
```

```bash
curl -sS -X POST 'http://10.210.0.49:5024/api/core/skills/REPLACE_WITH_SKILL_ID/rollback' \
  -H 'X-User-Id: user_001' \
  -H 'X-User-Name: 张三' \
  -H 'Content-Type: application/json' \
  -d '{"target_revision_id":"REPLACE_WITH_REVISION_ID"}'
```

## 8. RemoteFS

RemoteFS 路径格式是 `skills/<category>/<skill_name>/<relative_path>`。下面用内置 deep research 技能的常见目录名作为示例；如果名称不同，请按 `/skills` 响应里的 `category` 和 `skill_name` 手动替换 URL path 参数。

```bash
curl -sS 'http://10.210.0.49:5024/api/core/remote-fs/list?path=skills/research/deep-research&user_id=user_001&task_id=task-curl-001' \
  -H 'X-User-Id: user_001' \
  -H 'X-User-Name: 张三'
```

```bash
curl -sS 'http://10.210.0.49:5024/api/core/remote-fs/info?path=skills/research/deep-research/SKILL.md&user_id=user_001&task_id=task-curl-001' \
  -H 'X-User-Id: user_001' \
  -H 'X-User-Name: 张三'
```

```bash
curl -sS 'http://10.210.0.49:5024/api/core/remote-fs/exists?path=skills/research/deep-research/SKILL.md&user_id=user_001&task_id=task-curl-001' \
  -H 'X-User-Id: user_001' \
  -H 'X-User-Name: 张三'
```

```bash
curl -sS 'http://10.210.0.49:5024/api/core/remote-fs/content?path=skills/research/deep-research/SKILL.md&user_id=user_001&task_id=task-curl-001&encoding=base64' \
  -H 'X-User-Id: user_001' \
  -H 'X-User-Name: 张三'
```

```bash
curl -sS -X PUT 'http://10.210.0.49:5024/api/core/remote-fs/content?path=skills/research/deep-research/references/remote-fs-curl.md&user_id=user_001&task_id=task-curl-001' \
  -H 'X-User-Id: user_001' \
  -H 'X-User-Name: 张三' \
  -H 'Content-Type: text/markdown' \
  --data-binary '# RemoteFS curl check

Written through /remote-fs/content.
'
```

```bash
curl -sS -X POST 'http://10.210.0.49:5024/api/core/remote-fs/move?user_id=user_001&task_id=task-curl-001' \
  -H 'X-User-Id: user_001' \
  -H 'X-User-Name: 张三' \
  -H 'Content-Type: application/json' \
  -d '{"from":"skills/research/deep-research/references/remote-fs-curl.md","to":"skills/research/deep-research/references/remote-fs-curl-renamed.md"}'
```

```bash
curl -sS -X DELETE 'http://10.210.0.49:5024/api/core/remote-fs/path?path=skills/research/deep-research/references/remote-fs-curl-renamed.md&user_id=user_001&task_id=task-curl-001' \
  -H 'X-User-Id: user_001' \
  -H 'X-User-Name: 张三'
```

## 9. 自动生成、预览、确认、丢弃

这些接口会调用模型配置和算法服务。如果环境未配置模型，可能返回语义错误码或上游错误。

```bash
curl -sS -X POST 'http://10.210.0.49:5024/api/core/skills/REPLACE_WITH_SKILL_ID:generate' \
  -H 'X-User-Id: user_001' \
  -H 'X-User-Name: 张三' \
  -H 'Content-Type: application/json' \
  -d '{"user_instruct":"把这个技能补充一个 curl smoke test 说明段落"}'
```

```bash
curl -sS 'http://10.210.0.49:5024/api/core/skills/REPLACE_WITH_SKILL_ID:draft-preview' \
  -H 'X-User-Id: user_001' \
  -H 'X-User-Name: 张三'
```

```bash
curl -sS -X POST 'http://10.210.0.49:5024/api/core/skills/REPLACE_WITH_SKILL_ID:confirm' \
  -H 'X-User-Id: user_001' \
  -H 'X-User-Name: 张三' \
  -H 'Content-Type: application/json' \
  -d '{}'
```

```bash
curl -sS -X POST 'http://10.210.0.49:5024/api/core/skills/REPLACE_WITH_SKILL_ID:discard' \
  -H 'X-User-Id: user_001' \
  -H 'X-User-Name: 张三' \
  -H 'Content-Type: application/json' \
  -d '{}'
```

## 10. 分享

```bash
curl -sS -X POST 'http://10.210.0.49:5024/api/core/skills/REPLACE_WITH_SKILL_ID:share' \
  -H 'X-User-Id: user_001' \
  -H 'X-User-Name: 张三' \
  -H 'Content-Type: application/json' \
  -d '{"target_user_ids":["user_002"],"message":"curl share smoke test"}'
```

从响应中记录 `data.items[0].share_item_id`。

```bash
curl -sS 'http://10.210.0.49:5024/api/core/skills/REPLACE_WITH_SKILL_ID:shares?page=1&page_size=20' \
  -H 'X-User-Id: user_001' \
  -H 'X-User-Name: 张三'
```

```bash
curl -sS 'http://10.210.0.49:5024/api/core/skill-shares/outgoing?page=1&page_size=20' \
  -H 'X-User-Id: user_001' \
  -H 'X-User-Name: 张三'
```

```bash
curl -sS 'http://10.210.0.49:5024/api/core/skill-shares/incoming?page=1&page_size=20' \
  -H 'X-User-Id: user_002' \
  -H 'X-User-Name: 李四'
```

```bash
curl -sS 'http://10.210.0.49:5024/api/core/skill-shares/REPLACE_WITH_SHARE_ITEM_ID' \
  -H 'X-User-Id: user_002' \
  -H 'X-User-Name: 李四'
```

```bash
curl -sS -X POST 'http://10.210.0.49:5024/api/core/skill-shares/REPLACE_WITH_SHARE_ITEM_ID:accept' \
  -H 'X-User-Id: user_002' \
  -H 'X-User-Name: 李四' \
  -H 'Content-Type: application/json' \
  -d '{}'
```

## 11. 市场

市场上架依赖已上传 ZIP 的 `upload_id`，下面命令用于验证列表、详情和安装。先查列表，记录 `data.items[0].market_item_id`。

```bash
curl -sS 'http://10.210.0.49:5024/api/core/skill-market?page=1&page_size=20' \
  -H 'X-User-Id: user_001' \
  -H 'X-User-Name: 张三'
```

```bash
curl -sS 'http://10.210.0.49:5024/api/core/skill-market/REPLACE_WITH_MARKET_ITEM_ID' \
  -H 'X-User-Id: user_001' \
  -H 'X-User-Name: 张三'
```

```bash
curl -sS -X POST 'http://10.210.0.49:5024/api/core/skill-market/REPLACE_WITH_MARKET_ITEM_ID:install' \
  -H 'X-User-Id: user_002' \
  -H 'X-User-Name: 李四' \
  -H 'Content-Type: application/json' \
  -d '{}'
```

管理员上架示例。把 `REPLACE_WITH_UPLOAD_ID` 手动替换为已完成的 ZIP 上传 ID。

```bash
curl -sS -X POST 'http://10.210.0.49:5024/api/core/admin/skill-market' \
  -H 'X-User-Id: admin_001' \
  -H 'X-User-Name: 管理员' \
  -H 'Content-Type: application/json' \
  -d '{"name":"curl-market-skill","category":"curl","source":{"type":"uploaded_zip","upload_id":"REPLACE_WITH_UPLOAD_ID"}}'
```

```bash
curl -sS -X PATCH 'http://10.210.0.49:5024/api/core/admin/skill-market/REPLACE_WITH_MARKET_ITEM_ID' \
  -H 'X-User-Id: admin_001' \
  -H 'X-User-Name: 管理员' \
  -H 'Content-Type: application/json' \
  -d '{"version_note":"curl market edit smoke test"}'
```

```bash
curl -sS -X POST 'http://10.210.0.49:5024/api/core/admin/skill-market/REPLACE_WITH_MARKET_ITEM_ID:offline' \
  -H 'X-User-Id: admin_001' \
  -H 'X-User-Name: 管理员' \
  -H 'Content-Type: application/json' \
  -d '{}'
```

## 12. 语义错误码冒烟验证

非法 path 应返回 `data.code=invalid_path`。

```bash
curl -sS -X PUT 'http://10.210.0.49:5024/api/core/skills/REPLACE_WITH_SKILL_ID/draft/fs/text' \
  -H 'X-User-Id: user_001' \
  -H 'X-User-Name: 张三' \
  -H 'Content-Type: application/json' \
  -d '{"path":"../evil.md","content":"bad","expected_draft_version":1}'
```

空草稿提交应返回 `data.code=empty_draft`。

```bash
curl -sS -X POST 'http://10.210.0.49:5024/api/core/skills/REPLACE_WITH_SKILL_ID/commit' \
  -H 'X-User-Id: user_001' \
  -H 'X-User-Name: 张三' \
  -H 'Content-Type: application/json' \
  -d '{"draft_version":1}'
```

草稿版本过期应返回 `data.code=draft_version_conflict`。

```bash
curl -sS -X PUT 'http://10.210.0.49:5024/api/core/skills/REPLACE_WITH_SKILL_ID/draft/fs/text' \
  -H 'X-User-Id: user_001' \
  -H 'X-User-Name: 张三' \
  -H 'Content-Type: application/json' \
  -d '{"path":"references/stale-version.md","content":"stale","expected_draft_version":999999}'
```

## 13. 2026-07-07 部署实测结果

测试环境：`http://10.210.0.49:5024/api/core`

认证方式：`Authorization: Bearer <token>`。根路径 `http://10.210.0.49:5024/skills` 返回前端 HTML，后端接口使用 `/api/core` 前缀。

测试 Skill：

- `skill_id`: `2b0eb17e-be5d-4e0e-8510-5c17b36182d2`
- `skill_name`: `deep-research`
- 初始 `head_revision_id`: `6835b082-16d5-4137-a836-6e074f5b4d0f`
- 初始草稿状态：`draft_version=1`，`has_uncommitted_draft=false`，`overlay_count=0`

### 13.1 Draft Review 主链路

| 步骤 | 接口 | 结果 |
| --- | --- | --- |
| 查询列表 | `GET /skills?page=1&page_size=1` | `200 OK`，`code=0`，返回列表数据 |
| 查询草稿状态 | `GET /skills/{skill_id}/draft/status` | `200 OK`，`draft_version=1`，`has_uncommitted_draft=false` |
| 写入测试 draft | `PUT /skills/{skill_id}/draft/fs/text` | `200 OK`，返回 `draft_version=2`、`blob_hash=713decab02b9e3a46bdd588a196fffb7b9d4d15cd3192e06d78cd07fe66881a1` |
| 生成 file diff | `POST /skill-diff/file` | `200 OK`，返回 `review_id=7b78ba77-1098-46de-9444-f32788cfd950`、`review_version=1`、`draft_version=2`、`hunk_count=1`、`pending_count=1`、`can_undo=false` |
| 获取 hunk | `POST /skill-diff/file` 响应中的 HUNK 行 | `hunk_id=hunk_0001_16370635f39d`，`decision=pending` |
| 接受 hunk | `POST /skills/{skill_id}/draft-review/{review_id}/actions` | `200 OK`，返回 `batch_id=eee05119-0aaf-44c4-b479-72d771012710`、`review_version=2`、`can_undo=true` |
| 撤销上一步 | `POST /skills/{skill_id}/draft-review/{review_id}:undo` | `200 OK`，返回 `review_version=3`、`undone_batch_id=eee05119-0aaf-44c4-b479-72d771012710`、`items[0].decision=pending`、`can_undo=false` |
| 拒绝 hunk | `POST /skills/{skill_id}/draft-review/{review_id}/actions` | `200 OK`，返回 `batch_id=0b9b25b3-9ecb-43dd-aa5e-100116204e67`、`review_version=4`、`can_undo=true` |
| 提交 review | `POST /skills/{skill_id}/draft-review/{review_id}:commit` | `200 OK`，生成正式 revision：`revision_id=52af590a-95fe-4ff1-ae58-00d9f0098364`、`revision_no=2` |
| 提交后草稿状态 | `GET /skills/{skill_id}/draft/status` | `200 OK`，`base_revision_id=52af590a-95fe-4ff1-ae58-00d9f0098364`、`draft_version=3`、`has_uncommitted_draft=false`、`overlay_count=0` |
| 提交后继续操作旧 review | `POST /skills/{skill_id}/draft-review/{review_id}/actions` | `404 Not Found`，响应 `code=2000106`，`data.code=not_found`，说明旧 `review_id` 已结束生命周期 |
| 读取正式文件 | `GET /skills/{skill_id}/file?path=SKILL.md` | `200 OK`，`blob_hash=e4952addd1a96eb7782d3eaf0d01263170dd2f3868ad97f21441c2550f22bda1`，内容开头为原始 `deep-research` frontmatter |

### 13.2 观察到的问题

| 问题 | 表现 | 影响 |
| --- | --- | --- |
| 5024 端口偶发拒连 | 多次出现 `curl: (7) Failed to connect to 10.210.0.49 port 5024` | 影响连续脚本稳定性；单步重试后主链路通过 |
| 根路径不是后端 API | `GET /skills` 返回前端 HTML | curl 文档中的地址已调整为 `/api/core` |
| 大 diff 响应体较大 | 将 `SKILL.md` 覆盖成短文本后，`/skill-diff/file` 响应包含大量 deletion line | 记录结果时需要用 `jq` 摘要字段，避免输出完整 diff |
