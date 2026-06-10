# M5 — AI 写作插件

## 概述

**里程碑目标**：使用 M1–M4 已验证的框架原语，组装 AI 写作插件。重点在内容侧（工作流编排、prompt 设计、前端布局），框架层无需改动。

**前置依赖**：M4 完成（所有 UI 原语就绪，大文本 OSS，Go 上下文注入）。

**设计原则**：插件开发者只关注 `plugins/writing-plugin/` 目录，不需要修改框架代码（除 `registry.ts` 加一行 import）。

**验收标准（一句话）**：human 模式下大纲审核通过 → 继续 → draft 流式生成 → 引用段落修改 → 版本回退；auto 模式下 `driver.md` 驱动三个步骤自动推进完成。

---

## 一、需要实现的功能

### 1.1 插件目录结构

```
plugins/writing-plugin/
  plugin.yaml
  scenario/
    scenario.md
    state.yml
    driver.md
    prompts/
      outline.md
      draft.md
      final.md
  algorithm.py
  frontend/
    config.ts
    types.ts
    index.tsx
  backend/
    export.go       # Word/PDF 导出
```

### 1.2 `plugin.yaml` 定义

```yaml
id: writing-plugin
name: AI 写作
scenario: scenario/
trigger_description: |
  Launch an interactive writing panel when the user asks to write a
  long-form article, essay, report, blog post, research paper, or any
  structured document longer than a few paragraphs.
steps:
  - id: outline
    label: 大纲
    default_mode: human    # 等待用户确认大纲
  - id: draft
    label: Draft
    default_mode: human    # 等待用户确认 draft
  - id: final
    label: 完稿
    default_mode: auto     # 完稿后 agent 自动判断质量
artifacts:
  global_info:     { type: text }    # 「全局信息」，全程注入上下文
  outline:         { type: json }    # 大纲树结构
  references:      { type: json }    # RAG 参考文献（框架自动收集）
  sub_questions:   { type: json }    # 子问题（框架自动收集）
  draft:           { type: text }    # Draft 全文（可能 >64KB，OSS 存储）
  final:           { type: text }    # 完稿全文（可能 >64KB，OSS 存储）
```

### 1.3 Scenario 文件

**`scenario/scenario.md`**

```markdown
# AI 写作插件

## 场景描述
帮助用户完成长篇文章写作。工作流：大纲（outline）→ Draft → 完稿（final）。
生成过程中的全局信息（global_info）会持续注入上下文。

## 各步骤能力
- outline：根据用户需求和全局信息生成结构化大纲，支持修改和重新生成
- draft：基于确认的大纲流式生成全文，支持引用特定段落进行局部修改
- final：对 draft 进行排版优化和润色，输出完稿

## 用户意图识别
当用户表达「满意/继续/没问题/可以/开始写」等确认意图时，调用 plugin_proceed()
当用户指向具体内容修改（段落、章节、措辞）时，调用 plugin_edit()
当用户引用了特定段落（cited_text 不为空）且提出修改意见时，调用 plugin_edit()
当用户提出一般性问题或闲聊时，直接回答，不调用插件工具
```

**`scenario/state.yml`**

```yaml
initial: outline
transitions:
  outline:
    - to: draft
      condition: "用户明确确认大纲，或表示满意并要求开始写作"
    - to: outline
      condition: "用户要求修改大纲方向、调整章节结构或重新生成大纲"
  draft:
    - to: final
      condition: "用户确认 draft 整体没有大问题，可以生成完稿"
    - to: draft
      condition: "用户要求修改 draft 的局部段落或整体方向"
  final:
    - to: draft
      condition: "用户对完稿不满意，需要回退重新修改 draft"
    - to: final
      condition: "用户要求对完稿做局部调整或排版修改"
```

**`scenario/driver.md`**（auto 模式决策策略）

```markdown
# Auto Driver — AI 写作插件

你是写作插件的自动决策助手。当前步骤已完成，请根据产物质量决定是否推进。

## outline 步骤判断标准
- 大纲覆盖用户原始需求的主要方面
- 章节数在 3-7 个之间，层级合理
- 满足条件 → 调用 plugin_proceed(target_step="draft")
- 有明显缺失或偏题 → 调用 plugin_edit 要求补充

## draft 步骤判断标准
- 所有大纲章节均已生成，无明显空白段落
- 总字数与大纲规模匹配（每个二级标题下至少 200 字）
- 满足条件 → 调用 plugin_proceed(target_step="final")
- 有明显空白 → 调用 plugin_edit 要求补全

## final 步骤判断标准
- 完稿与 draft 相比有明显改善（格式、流畅度）
- 无明显语病或乱码
- 满足条件 → 不调任何工具（流程结束）

## 原则
- 不主动提出超出用户需求范围的修改
- 每步最多循环修改 2 次，超出则强制推进
- 不确定时倾向于推进而非阻塞
```

**`scenario/prompts/outline.md`**

```markdown
# 大纲生成

## 任务
根据用户需求和全局信息，生成一份结构化文章大纲。

## 全局信息
{global_info}

## 用户需求
{user_input}

## 输出格式
以 JSON 输出，格式如下：
{
  "title": "文章标题",
  "nodes": [
    { "id": "1", "title": "第一章标题", "level": 1, "children": [
      { "id": "1.1", "title": "1.1 子章节", "level": 2, "children": [], "summary": "本节概述..." }
    ]}
  ]
}

## 要求
- 章节数 3-7 个，层级不超过 2 级
- 每个叶节点提供 summary 说明该节要写什么
- 逻辑清晰，层次分明
```

**`scenario/prompts/draft.md`** 和 **`scenario/prompts/final.md`** 类似结构，分别包含 draft 生成和完稿润色的 prompt 模板。

### 1.4 `algorithm.py` 实现

**三步骤工作流**

```python
@register_plugin
class WritingPlugin(BasePlugin):
    plugin_id = 'writing-plugin'

    def run(self, ctx: PluginContext):
        step = ctx.step
        
        if step == 'outline':
            yield from self._run_outline(ctx)
        elif step == 'draft':
            yield from self._run_draft(ctx)
        elif step == 'final':
            yield from self._run_final(ctx)

    def _run_outline(self, ctx):
        prompt_tpl = self.load_prompt('outline')
        global_info = ctx.plugin_context.get('global_info', '')
        prompt = prompt_tpl.format(
            global_info=global_info,
            user_input=ctx.plugin_context.get('input', '')
        )
        # 调纯函数工具
        outline_json = call_llm(prompt, response_format='json')
        
        yield PluginEvent(
            type='patch',
            plugin_session_id=ctx.plugin_session_id,
            artifact_id='outline',
            op='replace',
            value=outline_json
        )
        yield PluginEvent(
            type='step_change',
            plugin_session_id=ctx.plugin_session_id,
            step='outline',
            step_status='waiting'
        )

    def _run_draft(self, ctx):
        prompt_tpl = self.load_prompt('draft')
        # 获取已确认的 outline artifact（框架提供 ctx.get_artifact）
        outline = ctx.get_artifact('outline')
        global_info = ctx.plugin_context.get('global_info', '')
        cited_text = ctx.payload.get('cited_text', '')
        
        prompt = prompt_tpl.format(
            global_info=global_info,
            outline=json.dumps(outline),
            cited_text=cited_text,
            modification_instruction=ctx.plugin_context.get('input', '')
        )
        # 流式生成 draft
        for chunk in call_llm_stream(prompt):
            yield PluginEvent(
                type='patch',
                plugin_session_id=ctx.plugin_session_id,
                artifact_id='draft',
                op='append',
                value=chunk
            )
        yield PluginEvent(
            type='step_change',
            plugin_session_id=ctx.plugin_session_id,
            step='draft',
            step_status='waiting'
        )

    def _run_final(self, ctx):
        # 类似 draft，对 draft artifact 内容做润色
        ...
```

**关键约束**：
- `algorithm.py` 只调纯函数工具（`call_llm`、`call_llm_stream`、`search_kb`），不引用框架类
- 不负责版本快照（框架处理）
- 不负责 Redis 信号（框架处理）

### 1.5 前端实现

**`frontend/types.ts`**

```ts
export interface OutlineArtifact {
  title: string
  nodes: OutlineNode[]
}

export interface DraftArtifact {
  content: string     // Markdown 全文
}

export interface FinalArtifact {
  content: string
}

export interface GlobalInfo {
  content: string
}
```

**`frontend/index.tsx`** — 写作插件主视图

```tsx
export default function WritingPlugin({ sessionId }: { sessionId: string }) {
  const session = usePluginSession(sessionId)
  const [activeTab, setActiveTab] = useState<'outline' | 'draft' | 'references' | 'sub_questions'>('outline')

  return (
    <PluginShell
      pluginSessionId={sessionId}
      title="AI 写作"
      sidebar={<SidebarTOC content={session.artifacts.draft?.content} />}
    >
      <GlobalInfoPanel artifact={session.artifacts.global_info} sessionId={sessionId} />
      
      <StepProgress sessionId={sessionId} />
      
      <TabBar
        tabs={['大纲', 'Draft', '参考文献', '子问题']}
        active={activeTab}
        onChange={setActiveTab}
      />
      
      {activeTab === 'outline' && (
        <OutlineTree
          artifact={session.artifacts.outline}
          onReorder={(newOutline) => patchArtifact(sessionId, 'outline', newOutline)}
        />
      )}
      {activeTab === 'draft' && (
        <RichEditor
          artifact={session.artifacts.draft}
          onEdit={(content) => patchArtifact(sessionId, 'draft', { content })}
        />
      )}
      {activeTab === 'references' && (
        <ReferencePanel artifact={session.artifacts.references} />
      )}
      {activeTab === 'sub_questions' && (
        <SubQuestionList artifact={session.artifacts.sub_questions} />
      )}
      
      <VersionHistory sessionId={sessionId} artifactId={activeTab} />
      
      <ExportButton sessionId={sessionId} />
    </PluginShell>
  )
}
```

**`GlobalInfoPanel` 组件**

- 展示 `global_info` artifact 内容，允许用户编辑
- 顶部固定展示，折叠/展开
- 说明：「全局信息将在整个写作过程中持续注入 AI 上下文」

**导出按钮**

- 触发 `GET /api/v1/plugin-sessions/:id/export?format=docx|pdf`
- 在 `backend/export.go` 中实现

**`frontend/config.ts`**

```ts
export const writingPluginConfig = {
  id: 'writing-plugin',
  name: 'AI 写作',
  icon: 'PenLine',
  triggerKeywords: ['写文章', '写报告', '写博客', '撰写', 'write', 'article', 'essay']
}
```

### 1.6 `backend/export.go`

实现 Word/PDF 导出：

```go
func init() {
    // 注册导出路由
    RegisterExportRoute("writing-plugin", handleExport)
}

func handleExport(c *gin.Context) {
    format := c.Query("format") // "docx" or "pdf"
    sessionID := c.Param("id")
    
    // 获取 final artifact（或 draft，若 final 未完成）
    artifact := getHeadArtifact(sessionID, "final")
    if artifact == nil {
        artifact = getHeadArtifact(sessionID, "draft")
    }
    
    switch format {
    case "docx":
        // 使用 go-docx 生成 Word 文档
    case "pdf":
        // 转换为 PDF
    }
}
```

### 1.7 `registry.ts` 注册

```ts
// frontend/src/modules/chat/plugins/registry.ts
import './writing-plugin'   // 新增这一行
```

---

## 二、实施计划

### 阶段划分

**Week 1 — Scenario 文件 + plugin.yaml**

| 任务 | 负责层 | 工作量 |
|------|--------|--------|
| `plugin.yaml` 编写 | Algorithm | 0.5d |
| `scenario.md` 编写 | Algorithm | 0.5d |
| `state.yml` 编写 | Algorithm | 0.5d |
| `driver.md` 编写 | Algorithm | 0.5d |
| `prompts/outline.md` + `draft.md` + `final.md` | Algorithm | 1.5d |
| `POST /plugins/writing-plugin/validate` 验证通过 | Algorithm | 0.5d |

**Week 2 — 算法层实现**

| 任务 | 负责层 | 工作量 |
|------|--------|--------|
| `algorithm.py`：`_run_outline` | Algorithm | 1d |
| `algorithm.py`：`_run_draft`（流式） | Algorithm | 1.5d |
| `algorithm.py`：`_run_final` | Algorithm | 0.5d |
| `ctx.get_artifact()` 框架方法补充（若 M4 未实现） | Algorithm | 0.5d |

**Week 3 — 前端视图**

| 任务 | 负责层 | 工作量 |
|------|--------|--------|
| `frontend/types.ts` | Frontend | 0.5d |
| `GlobalInfoPanel` 组件 | Frontend | 1d |
| 写作插件主视图 `index.tsx`（四 Tab 布局） | Frontend | 1.5d |
| `ExportButton` 组件 | Frontend | 0.5d |
| `registry.ts` 注册 | Frontend | 0.5h |

**Week 4 — 后端导出 + 联调**

| 任务 | 负责层 | 工作量 |
|------|--------|--------|
| `backend/export.go`（Word/PDF 导出） | Backend | 1.5d |
| 全流程联调（human 模式） | All | 1d |
| auto 模式联调（driver.md 驱动三步） | All | 1d |

---

## 三、测试方案

### 3.1 Scenario 文件校验

```bash
# 启动服务后，验证 Scenario 校验通过
POST /api/v1/plugins/writing-plugin/validate
期望响应：
{
  "is_valid": true,
  "errors": [],
  "warnings": [...]  # 允许有 warning
}
```

**state.yml 合法性检查清单**

- [ ] `initial: outline` 在 steps 中存在
- [ ] 所有 `to` 值（draft、final）在 steps 中存在
- [ ] transitions 形成连通图（outline/draft/final 均可达）
- [ ] steps ID 与 prompts/ 目录下文件名一致

### 3.2 算法层单元测试

```python
def test_outline_step_emits_correct_events():
    # mock call_llm 返回合法 JSON 大纲
    # 验证 emit 了 patch(artifact_id='outline') + step_change(status='waiting')

def test_draft_step_streams_content():
    # mock call_llm_stream 返回 chunks
    # 验证每个 chunk 都 emit patch(op='append')
    # 验证最后 emit step_change(status='waiting')

def test_final_step_emits_events():
    # 类似 draft

def test_cited_text_injected_into_draft_prompt():
    # ctx.payload.cited_text = '某段落'
    # 验证 draft prompt 中包含 cited_text 内容

def test_global_info_injected_into_all_prompts():
    # global_info artifact 有内容
    # 验证 outline/draft/final prompt 都包含 global_info
```

### 3.3 前端组件测试

```ts
describe('WritingPlugin', () => {
  it('renders 4 tabs', () => {
    expect(screen.getByText('大纲')).toBeVisible()
    expect(screen.getByText('Draft')).toBeVisible()
    expect(screen.getByText('参考文献')).toBeVisible()
    expect(screen.getByText('子问题')).toBeVisible()
  })

  it('GlobalInfoPanel is always visible', () => {
    // 切换 tab 时 GlobalInfoPanel 不消失
  })

  it('export button triggers download', async () => {
    // 点击导出，验证 fetch /export?format=docx 被调用
  })
})
```

### 3.4 端到端（E2E）验收测试

**human 模式完整流程**

```
1. 对话框输入：「帮我写一篇关于量子计算的博客文章，面向技术初学者」
2. 验证 PluginShell 弹出，StepProgress 显示「大纲 - 运行中」
3. 大纲生成完成
   → 验证 OutlineTree 中有 3-7 个节点
   → 验证 StepProgress 显示「大纲 - 等待确认」
   → 验证「继续」按钮出现
4. 对话框输入：「大纲不错，但第 3 章太简单了，帮我扩展一下」
   → 验证 agent 调用 plugin_edit(artifact_id='outline', ...)
   → 验证大纲更新，VersionHistory 有新版本
5. 点击「继续」（或发送「没问题继续」）
   → 验证 Draft Tab 切换，流式生成开始
6. Draft 生成完成（全文 > 1000 字）
   → 验证 RichEditor 展示完整内容
   → 验证 ReferencePanel 有参考文献条目
   → 验证 SubQuestionList 有子问题
7. 在 RichEditor 中选中一段，点击「引用」，发送「把这段改得更通俗易懂」
   → 验证 plugin_edit 被调用，cited_text 包含选中内容
   → 验证 draft 对应段落被修改
8. 确认 draft，推进到 final
9. final 生成完成
   → 验证 Final Tab 展示完稿
10. 点击导出 Word
    → 验证文件下载（.docx）
11. 刷新页面，验证所有内容保留
```

**auto 模式流程**

```
1. 所有步骤切换为 auto 模式
2. 触发写作任务
3. 验证三个步骤自动推进（无需用户交互）
4. 检查 driver.md 中的判断标准是否生效
   （如大纲章节数 < 3 时应 plugin_edit 要求补充）
5. 最终 final 生成完成
```

**版本回退**

```
1. draft 修改 2 次（v1 → v2 → v3）
2. VersionHistory 回退到 v1
3. 验证 RichEditor 显示 v1 内容
4. 在 v1 基础上继续修改（分叉）
5. 验证 v2、v3 仍可在版本历史中查到
```

---

## 四、验收标准

| 验收项 | 验收方式 | 通过标准 |
|--------|----------|----------|
| Scenario 校验通过 | validate API | is_valid=true，无 error |
| 大纲生成并展示 | E2E | OutlineTree 有正确节点数 |
| 用户意图正确路由 | E2E | 继续→proceed，修改→edit，闲聊→直接回复 |
| Draft 流式生成 | E2E | RichEditor 逐渐显示内容 |
| 选中引用 + 修改 | E2E | cited_text 正确传给 AI，内容更新 |
| 参考文献/子问题自动收集 | E2E | 对应 Tab 有内容 |
| 版本回退生效 | E2E | 回退后内容正确 |
| auto 模式三步自动推进 | E2E | 无用户干预完成全流程 |
| Word/PDF 导出 | E2E | 文件可下载且内容完整 |
| 刷新后数据保留 | E2E | 所有 artifact 刷新后正确 |
| 不影响图片插件 | E2E | 两个插件同时运行互不干扰 |

---

## 五、注意事项与风险

1. **draft 流式生成的 artifact 大小**：长文章可能超过 64KB，需确认 M4 的 OSS 存储在写作插件中正常工作，流式 append 过程中无需实时 OSS，只在 patch 完成打快照时才切 OSS。
2. **`global_info` 的上下文注入时机**：用户可以在任何时候编辑 `global_info`，需确保下次 step 执行时 `algorithm.py` 读取的是最新版本（通过 `ctx.get_artifact('global_info')` 从 DB 实时读取，而非从 session 启动时的缓存）。
3. **prompt 长度控制**：当大纲节点很多时，`draft.md` prompt 中的 outline 部分可能很长，需要在 prompt 模板中做长度限制（截断或摘要）。
4. **引用段落修改的精确性**：`cited_text` 只是文本内容，不包含位置信息；AI 需要在整篇 draft 中定位对应段落，建议在 prompt 中明确要求「只修改与引用文本完全匹配的段落，其余部分保持不变」。
5. **`backend/export.go` 依赖**：需引入 `go-docx` 或类似库，确认 license 兼容性。
