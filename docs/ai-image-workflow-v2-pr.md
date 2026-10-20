# AI Image Workflow V2：PR 改动、冲突与本地验证说明

> 文档状态：PR 提交前本地审查版
> PR 目标基线：`CarlosShaoting/LazyRAG:workflow_dev@390d1505`
> 本地参考基线：当前 8091 部署对应的 `LazyAGI/LazyMind@b6278221`
> 审查日期：2026-09-24

## 1. 结论摘要

本次 PR 的有效代码范围已经收敛为：新增一个独立的内置工作流 `image-workflow-v2`、补充工作流所需的最小图片模型调用能力，以及对应的专项测试。

与 PR 目标分支及当前 8091 部署代码对比后：

- 已将全部改动重放到 `workflow_dev@390d1505`，PR 不会夹带另一条主线上的 Feishu 修复提交；
- 重放过程没有产生文本冲突，并已完成与已合入 PPT PR 的语义冲突审查；
- 新工作流使用独立目录，不覆盖现有 `image-workflow`；
- 唯一共享的生产代码改动保持原有调用接口和返回结构兼容；
- 没有前端源码、右侧子任务展示、精选入口、回放功能或 LazyLLM 子模块改动；
- 与本次改动直接相关的 Python、Go、前端兼容性检查均通过；
- Windows 上的 `backend/core` 全量 Go 测试未全绿，失败可在未修改的 8091 基线复现，主要由 `CGO_ENABLED=0`、SQLite 文件句柄和 Unix 信号测试造成，不是本 PR 引入的回归；
- 普通生图已经完成一次真实端到端验证；局部编辑、静态表情包和动态表情包尚未在最终分支重新做真实验收，应在合并前或后续联调阶段补齐。

当前状态适合提交 PR 供研发评审，但不应在 PR 描述中宣称四条实际任务场景均已验收完成。

## 2. 对比基线

### 2.1 PR 目标分支

- 仓库：`CarlosShaoting/LazyRAG`
- 分支：`workflow_dev`
- 基线提交：`390d15057f7c964de2ac25ea9d5c5180b851e1cc`
- 提交说明：`Merge pull request #5 from gagaein/codex/ppt-workflow-concurrency-and-editing`

### 2.2 最新本地部署参考

- 页面：`http://127.0.0.1:8091/agent/chat/home`
- 源码目录：`C:\Users\chenxiaoyu\Documents\Codex\LazyMind-upstream-8091`
- 分支：`main`
- Git 状态：干净，跟踪 `origin/main`
- 基线提交：`b6278221145d662a124a77012b0d6fbca5569ab3`
- 提交说明：`fix(feishu): restore read/write authorization and preserve legacy OAuth (#763)`

### 2.3 待提交工作区

- 源码目录：`C:\Users\chenxiaoyu\Documents\Codex\LazyMind-ai-image-pr`
- 分支：`codex/ai-image-workflow-v2`
- 基线提交：`390d15057f7c964de2ac25ea9d5c5180b851e1cc`

8091 本地部署与 PR 目标分支在 `aa27f0f9` 后分叉：8091 主线包含 `b6278221` Feishu 修复，`workflow_dev` 包含已合并的 PPT PR。待提交工作区已经直接建立在 `workflow_dev@390d1505` 上，因此 GitHub PR 只会显示 AI Image Workflow V2 的 11 个文件，不会带入任一无关提交。

## 3. PR 目标

新增正式内置工作流 `image-workflow-v2`，按照最终交付物将 AI 图片任务拆分成三条互斥分支：

1. 普通生图；
2. 图片局部编辑；
3. 静态或动态表情包生产。

普通生图和局部编辑经过工作流包内的 Baoyu 适配层调用 Seedream 5.0；表情包分支保留 LazyMind 原有图片、视频、FFmpeg 和确定性字幕工具链。

本 PR 不替换原有 `image-workflow`，而是增加可独立加载、评审和验证的 V2 工作流。

## 4. Workflow 结构与状态组合

工作流先由 `classify_request` 根据最终交付物选择唯一分支。上传图片或搜索素材只作为输入来源，不改变最终交付物的判定优先级。

| 用户目标 | 路由 | 角色页嘴眼锚定 | 图片制作页动态图 | 最终产物 |
| --- | --- | --- | --- | --- |
| 普通生图 | `ordinary` | 不生成、不展示 | 不生成、不展示 | 单张图片 |
| 局部编辑 | `edit` | 不生成、不展示 | 不生成、不展示 | 编辑后的新图片 |
| 静态表情包 | `meme_static` | 生成并展示 | 不生成、不展示 | 静态表情包 |
| 动态表情包 | `meme_dynamic` | 生成并展示 | 生成并展示 | 动态 GIF 表情包 |

工作流面板声明四个页签：

1. 方案；
2. 角色；
3. 图片制作；
4. 最终结果。

其中第三个节点已统一命名为“图片制作”。空内容由现有面板的声明式隐藏能力处理，本 PR 没有修改前端组件。

## 5. 三条分支的具体改动

### 5.1 普通生图

执行路径：

```text
classify_request → ordinary_prepare → ordinary_generate → end
```

主要行为：

- 从不可变的启动请求确定性生成素材摘要和生图提示词，避免准备步骤替换主题或遗漏约束；
- 识别用户明确要求的画面比例，未指定时默认 1:1；
- 通过 Baoyu 适配层固定调用 `doubao-seedream-5-0-260128`；
- 上传图或素材图可以作为参考，但不会因此将普通生图误判为局部编辑；
- Provider 返回真实本地图片文件后才发布 `ordinary_output`；
- 不自动切换 Provider，也不回退到通用 `image_generator`。

### 5.2 图片局部编辑

执行路径：

```text
classify_request → edit_prepare → edit_candidate → end
```

主要行为：

- 只接受一个权威源图；
- 编辑前生成包含 `requested_edit`、`edit_scope`、`preserve` 和 `do_not` 的编辑契约；
- 默认保持源图比例，只有用户明确指定支持的比例时才改变；
- 通过 Baoyu 适配层调用 Seedream 5.0 图片编辑能力；
- 新图片发布为 `edit_candidate`，不覆盖原图；
- 调用失败时不创建占位图片、伪造 URL 或错误的成功状态。

### 5.3 表情包

静态与动态表情包共享以下前置流程：

```text
meme_brief
→ meme_preflight
→ canonical_character
→ build_identity_anchors
→ understand_states
→ design_best_performances
```

公共能力包括：

- 确认静态或动态模式、数量、状态、动作和字幕方案；
- 建立唯一标准角色和角色身份描述；
- 生成“睁眼/闭眼 × 张嘴/闭嘴”四张锚定图及 2×2 审批图；
- 为每个状态明确沟通目的、动作和表情；
- 媒体生成阶段不直接绘制字幕，字幕在后处理阶段由本地确定性工具添加。

静态表情包路径：

```text
plan_static → render_static → caption_static → review_pack → end
```

动态表情包路径：

```text
plan_dynamic
→ render_keyframes
→ video_canary
→ render_dynamic
→ caption_dynamic
→ review_pack
→ end
```

动态分支先生成一个视频样片，再生成其余视频，最后转换并合成带字幕 GIF。静态分支不会进入任何动态图节点。

## 6. Baoyu 与 Seedream 适配

工作流包新增 `scripts/baoyu.py`，为普通生图和局部编辑提供 Seedream 调用及可信产物发布能力。

关键约束：

- Provider 固定为 `seedream`；
- 模型固定为 `doubao-seedream-5-0-260128`；
- 不通过 shell、Bun 或 npx 启动外部进程；
- Seedream 5.0 请求不发送已废弃的 `guidance_scale`；
- 工作流图片尺寸显式映射到 Doubao 请求字段 `size`，避免静默回退到 1024×1024；
- 16:9 使用 2688×1512，9:16 使用 1512×2688，以满足 Seedream 像素限制；
- 复用 LazyMind 现有图片产物搬运、签名 URL 和来源登记逻辑；
- 只有 Provider 返回并成功落盘的真实图片才能成为 Workflow artifact。

共享图片支持模块新增 `run_image_model_instance(...)`，原有 `run_image_model(...)` 继续保留，并转调新函数。既有调用方的参数、返回值和异常行为未被改变。

## 7. 安全与一致性约束

- 路由、普通生图提示词和最终图片产物由工作流包内发布工具原子写入；
- 模型不能自行写入受保护的 artifact slot；
- 生图或编辑失败时步骤保持失败，不会借助自造 URL 将任务标记为成功；
- 普通生图、局部编辑、静态表情包和动态表情包路由互斥；
- 否定词不会导致普通图片请求被误路由到表情包分支；
- 工作流包可以作为不可变 revision 加载，运行时不依赖本地草稿目录。

## 8. 明确保留的现有能力

- 原有 `image-workflow` 文件和行为不变；
- 通用 `image_generator`、`image_editor` 和既有模型角色解析逻辑不变；
- 表情包继续使用 LazyMind 原生图片、视频和 FFmpeg 能力；
- WorkflowPanel 和右侧子任务区域继续使用研发版本原有实现；
- 其他内置工作流和 LazyLLM 子模块指针不变。

## 9. 明确不包含的内容

- 前端源码或右侧子任务样式改动；
- Featured/精选能力入口改动；
- 回放功能；
- “职场文字抽象梗”或其他实验性模板；
- SQLite、SSE、聊天路由、任务状态或通用 WorkflowPanel 修复；
- 运行日志、数据库、测试图片和本地部署脚本；
- LazyLLM 子模块指针变更。

## 10. 文件与代码量

实现和专项测试共 10 个文件，不含本文档：

| 类型 | 文件数 | 新增 | 删除 |
| --- | ---: | ---: | ---: |
| V2 工作流包 | 5 | 2126 | 0 |
| 最小平台兼容改动 | 2 | 28 | 2 |
| 专项测试 | 3 | 593 | 0 |
| 合计 | 10 | 2747 | 2 |

主要实现文件：

- `workflows/image-workflow-v2/workflow.yaml`
- `workflows/image-workflow-v2/scenario/state.yml`
- `workflows/image-workflow-v2/scenario/scenario.md`
- `workflows/image-workflow-v2/scripts/baoyu.py`
- `workflows/image-workflow-v2/scripts/tools.py`
- `algorithm/lazymind/chat/engine/tools/infra/image_generation_support.py`
- `backend/core/workflow/graphengine/compiler_test.go`

专项测试：

- `tests/algorithm/chat/test_image_workflow_v2_baoyu.py`
- `tests/algorithm/chat/test_image_workflow_v2_tools.py`
- `tests/algorithm/chat/workflows/test_image_workflow_v2_contract.py`

本文档建议随 PR 一起提交；运行日志和其他本地验收记录不提交。

## 11. 冲突审查

### 11.1 Git 与文本冲突

- PR 功能分支直接建立在 `CarlosShaoting/LazyRAG:workflow_dev@390d1505` 上；
- 从原 8091 基线移植到 PR 目标分支时没有产生文本冲突；
- 与已合入 `workflow_dev` 的 PPT PR 文件逐项对比后，最终 PR 差异仍严格限制为 11 个 AI Image 文件；
- 变更文件中没有 `<<<<<<<`、`=======` 或 `>>>>>>>` 冲突标记；
- `git diff --check` 通过；
- 当前没有需要手工解决的 rebase/merge 冲突。

### 11.2 目录与职责冲突

- V2 位于独立目录 `workflows/image-workflow-v2`，不与原工作流文件重叠；
- 编译测试仅将 V2 加入内置工作流可编译列表；
- 前端没有代码差异，因此不会覆盖研发侧最新的右侧子任务展示；
- 共享图片模块的改动是向后兼容的函数抽取，旧入口继续存在。

### 11.3 处理结果

本轮没有发现必须修改业务实现才能解决的冲突。仅在本地检查中将 Go 测试文件按 `gofmt` 规范化；工作流行为未因冲突审查发生变化。

## 12. 本地 CI 与验证结果

### 12.1 与改动直接相关的检查

| 检查项 | 结果 | 说明 |
| --- | --- | --- |
| V2 与共享图片支持 Python 测试 | 通过 | 58 passed；1 条 `requests` 字符检测依赖警告 |
| 文档检查 | 通过 | 2 passed；与上项合计 60 passed |
| Python `flake8` | 通过 | `algorithm`、`backend`、`evo` |
| Workflow naming 检查 | 通过 | `scripts/check_workflow_naming.py` |
| Migration immutability 检查 | 通过 | `scripts/check_migration_immutability.py` |
| Test location 检查 | 通过 | `scripts/check_test_locations.py` |
| Go 工作流编译包测试 | 通过 | `go test ./workflow/graphengine` |
| Go 格式检查 | 通过 | 修改后的 Go 文件通过 `gofmt` |
| 前端类型检查 | 通过 | `pnpm typecheck` |
| WorkflowPanel 前端测试 | 通过 | 17 files，141 tests |
| OpenAPI 新鲜度检查 | 通过 | auth/core/scan/channel-gateway 均为 fresh |
| 前端生产构建 | 通过 | 成功生成 `frontend/dist/index.html`；仅有既有 Sass、chunk size 等警告 |
| Diff 与冲突标记检查 | 通过 | `git diff --check`；未发现冲突标记 |

本地使用的主要版本：Python 3.11.15、Go 1.26.5、Node.js 22.23.1、pnpm 11.19.0。

Windows 环境没有安装 `make` 和 `golangci-lint`，因此 `make lint` 是按其可执行组成项手工运行；其中 `golangci-lint` 的 depguard 子项未在本地执行，应由远程 Linux CI 补齐。

### 12.2 `backend/core` 全量 Go 测试

执行 `go test ./... -timeout 40m` 后，工作流图编译包通过，但全量套件未全绿。主要失败类型为：

- 当前 Go 二进制为 `CGO_ENABLED=0`，`go-sqlite3` 返回 stub 错误；
- Windows 下 SQLite 临时数据库仍被进程占用，测试清理阶段无法删除；
- 少量依赖 Unix 信号或特定运行环境的测试不适用于当前 Windows 会话。

在未修改的 8091 基线仓库上抽样运行 `agentinvocation` 和 `capability/internal/coreadapter`，可复现相同的 `CGO_ENABLED=0` / `go-sqlite3` 失败。因此这些失败不作为本 PR 的新增回归；与本 PR 直接相关的 `workflow/graphengine` 包已通过。

## 13. 已知兼容边界与风险

- 普通生图和编辑依赖 Seedream Provider 配置及有效凭据；
- 模型版本被固定为 `doubao-seedream-5-0-260128`，后续模型升级需要显式修改并重新验收；
- `baoyu.py` 是工作流内的适配实现，不代表运行时直接安装或调用本地 Codex Skill 包；
- V2 当前与原工作流并存，产品侧是否替换默认入口应由后续评审决定；
- 动态表情包依赖视频模型与 FFmpeg，运行成本和失败面明显高于静态路线；
- 在三项未完成的真实验收补齐前，建议 PR 标注为“功能实现完成、部分 E2E 待补”。

## 14. 建议提交方式

建议将本次内容收敛为一个功能提交，提交以下文件：

1. `workflows/image-workflow-v2/` 下 5 个工作流文件；
2. 两个最小平台兼容文件；
3. 三个 V2 专项测试文件；
4. 本说明文档。

不要提交本地运行数据、日志、生成图片、`frontend/dist`、依赖缓存或部署脚本。

建议 PR 描述中的验收表述：

> 已完成 AI Image Workflow V2 的三分支实现、专项自动化测试、工作流编译验证及普通生图真实任务验证。局部编辑、静态表情包和动态表情包的最终真实任务验收待补；Windows 全量 Go 测试中的 CGO/SQLite 环境失败可在未修改主干复现。

## 15. 最终判断

当前改动已经落在 `CarlosShaoting/LazyRAG:workflow_dev@390d1505` 上，不存在 Git 或文本冲突，语义上也未发现会覆盖 PPT PR、原工作流、前端展示或共享接口的冲突。相对目标分支只包含 11 个 AI Image Workflow V2 文件，提交内容干净且可评审。

可以进入 PR 提交流程。提交后应以远程 CI 结果为准，并继续补齐局部编辑、静态表情包和动态表情包三个真实任务验收。
