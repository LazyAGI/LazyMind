# M6 — AI PPT 插件

## 概述

**里程碑目标**：开发第二个业务插件，验证「新增插件只需关注内容侧，框架零改动」的设计原则。同时验证双插件并存时上下文不互相串台。

**前置依赖**：M5 完成（AI 写作插件已跑通，框架能力已充分验证）。

**设计原则**：PPT 插件的开发者只需填充 `plugins/ppt-plugin/` 目录，`registry.ts` 加一行 import，不触碰框架任何代码。

**验收标准（一句话）**：PPT 全流程跑通（大纲 → 逐页设计文字 → 逐页完稿图片）；写作插件和 PPT 插件同时运行时上下文不串台，会话隔离正确。

---

## 一、需要实现的功能

### 1.1 插件目录结构

```
plugins/ppt-plugin/
  plugin.yaml
  scenario/
    scenario.md
    state.yml
    driver.md
    prompts/
      outline.md
      design.md
      render.md
  algorithm.py
  frontend/
    config.ts
    types.ts
    index.tsx
  backend/
    export.go     # PPTX 导出
```

### 1.2 `plugin.yaml` 定义

```yaml
id: ppt-plugin
name: AI PPT
scenario: scenario/
trigger_description: |
  Launch an interactive PPT creation panel when the user asks to create,
  generate, or make a presentation, slides, or PowerPoint on any topic.
steps:
  - id: outline
    label: 大纲
    default_mode: human    # 等待用户确认大纲和页面规划
  - id: design
    label: 页面设计
    default_mode: auto     # 逐页生成设计文字，完成后自动推进
  - id: render
    label: 完稿渲染
    default_mode: auto     # 逐页生成图片，完成后结束
artifacts:
  global_info:   { type: text }    # 全局信息
  outline:       { type: json }    # PPT 大纲（含页数规划）
  pages:         { type: json }    # 页面列表（逐页追加）
  sub_questions: { type: json }    # 子问题（框架自动收集）
```

### 1.3 Scenario 文件

**`scenario/scenario.md`**

```markdown
# AI PPT 插件

## 场景描述
帮助用户创建演示文稿（PPT）。工作流：大纲 → 逐页设计文字 → 逐页完稿渲染图片。
全局信息（global_info）在整个生成过程中持续注入上下文。

## 各步骤能力
- outline：根据用户需求生成 PPT 大纲和页面规划（每页标题 + 要点）
- design：逐页生成设计文字（标题、正文、备注），支持对单页修改
- render：逐页调用图片生成工具，输出完稿图片；支持对单页图片修改

## 用户意图识别
当用户表达「满意/继续/没问题/开始设计/生成图片」时，调用 plugin_proceed()
当用户针对某页内容提出修改意见时，调用 plugin_edit()
当用户发送了带标注的图片（annotations 不为空）时，调用 plugin_edit()
当用户提出一般性问题时，直接回答，不调用插件工具
```

**`scenario/state.yml`**

```yaml
initial: outline
transitions:
  outline:
    - to: design
      condition: "用户确认大纲页面规划，同意开始逐页设计"
    - to: outline
      condition: "用户要求修改大纲或调整页面数量"
  design:
    - to: render
      condition: "用户确认所有页面的设计文字，同意生成完稿图片"
    - to: design
      condition: "用户要求修改某页或全部页面的设计内容"
  render:
    - to: design
      condition: "用户对某页图片不满意，需要修改设计文字后重新渲染"
    - to: render
      condition: "用户要求重新渲染某页图片（基于图片标注或文字描述）"
```

**`scenario/driver.md`**

```markdown
# Auto Driver — AI PPT 插件

## outline 步骤判断标准
- 大纲页数在 5-20 页之间，每页有清晰标题
- 满足条件 → plugin_proceed(target_step="design")
- 页数不足或标题缺失 → plugin_edit 要求补充

## design 步骤判断标准
- 所有页面都已生成设计文字（pages 数组长度 == outline 页数）
- 每页包含 title 和 content
- 满足条件 → plugin_proceed(target_step="render")
- 有页面缺失 → plugin_edit 要求补全

## render 步骤判断标准
- 所有页面都有 imageUrl（非空）
- 满足条件 → 不调任何工具（流程结束）
- 有图片生成失败的页面 → plugin_edit 要求重新渲染

## 原则
- 每步最多循环修改 2 次，超出则强制推进
- render 步骤图片生成失败时，最多重试 1 次
```

### 1.4 `algorithm.py` 实现

**三步骤工作流**，重点在 `design` 和 `render` 步骤使用 `append_item` 逐页追加：

```python
@register_plugin
class PPTPlugin(BasePlugin):
    plugin_id = 'ppt-plugin'

    def run(self, ctx: PluginContext):
        step = ctx.step
        if step == 'outline':
            yield from self._run_outline(ctx)
        elif step == 'design':
            yield from self._run_design(ctx)
        elif step == 'render':
            yield from self._run_render(ctx)

    def _run_outline(self, ctx):
        prompt_tpl = self.load_prompt('outline')
        global_info = ctx.get_artifact('global_info') or ''
        prompt = prompt_tpl.format(
            global_info=global_info,
            user_input=ctx.plugin_context.get('input', '')
        )
        outline = call_llm(prompt, response_format='json')
        yield PluginEvent(
            type='patch', artifact_id='outline',
            op='replace', value=outline,
            plugin_session_id=ctx.plugin_session_id
        )
        yield PluginEvent(
            type='step_change', step='outline',
            step_status='waiting',
            plugin_session_id=ctx.plugin_session_id
        )

    def _run_design(self, ctx):
        outline = ctx.get_artifact('outline')
        global_info = ctx.get_artifact('global_info') or ''
        prompt_tpl = self.load_prompt('design')
        
        # 逐页生成设计文字
        for page_outline in outline['pages']:
            prompt = prompt_tpl.format(
                global_info=global_info,
                page_title=page_outline['title'],
                page_points=page_outline['points'],
                full_outline=json.dumps(outline)
            )
            page_design = call_llm(prompt, response_format='json')
            page_item = {
                'id': page_outline['id'],
                'index': page_outline['index'],
                'title': page_design['title'],
                'content': page_design['content'],
                'notes': page_design.get('notes', ''),
                'imageUrl': None  # render 步骤填充
            }
            # 使用 append_item 逐页追加，前端逐页展示
            yield PluginEvent(
                type='append_item',
                artifact_id='pages',
                item=page_item,
                plugin_session_id=ctx.plugin_session_id
            )
        
        yield PluginEvent(
            type='step_change', step='design',
            step_status='waiting',
            plugin_session_id=ctx.plugin_session_id
        )

    def _run_render(self, ctx):
        pages = ctx.get_artifact('pages') or []
        
        for page in pages:
            # 判断是否需要重新渲染（如果已有 imageUrl 且未指定修改，跳过）
            if page.get('imageUrl') and not _is_page_to_rerender(ctx, page['id']):
                continue
            
            # 调纯函数工具生成图片
            render_prompt = _build_render_prompt(page)
            image_url = generate_image(render_prompt)
            
            # patch 单页的 imageUrl
            yield PluginEvent(
                type='patch',
                artifact_id='pages',
                op='merge',
                path=f'/id={page["id"]}',
                value={'imageUrl': image_url},
                plugin_session_id=ctx.plugin_session_id
            )
        
        yield PluginEvent(
            type='step_change', step='render',
            step_status='waiting',
            plugin_session_id=ctx.plugin_session_id
        )
```

**`append_item` 事件处理**

`append_item` 是 M1 中 `PluginEvent` 定义的类型，Go SSE Handler 收到后：
- 将 `item` 追加到 `pages` artifact 的 JSON 数组中
- 前端 `pluginSessionStore` 收到 `append_item` 事件后，直接追加到本地 `pages` 列表，`PageCarousel` 实时显示新增页面

### 1.5 前端实现

**`frontend/types.ts`**

```ts
export interface PPTOutlineItem {
  id: string
  index: number
  title: string
  points: string[]
}

export interface PPTOutlineArtifact {
  title: string
  pages: PPTOutlineItem[]
}

export interface PPTPage {
  id: string
  index: number
  title: string
  content: string      // Markdown 格式的页面设计文字
  notes?: string
  imageUrl?: string    // render 步骤完成后填充
}

export interface PPTArtifact {
  pages: PPTPage[]
}
```

**`frontend/index.tsx`** — PPT 插件主视图

```tsx
export default function PPTPlugin({ sessionId }: { sessionId: string }) {
  const session = usePluginSession(sessionId)
  const [view, setView] = useState<'outline' | 'design'>('outline')

  return (
    <PluginShell
      pluginSessionId={sessionId}
      title="AI PPT"
    >
      <GlobalInfoPanel artifact={session.artifacts.global_info} sessionId={sessionId} />
      
      <StepProgress sessionId={sessionId} />
      
      {session.currentStep === 'outline' && (
        <OutlineTree
          artifact={session.artifacts.outline}
          onReorder={(newOutline) => patchArtifact(sessionId, 'outline', newOutline)}
        />
      )}
      
      {(session.currentStep === 'design' || session.currentStep === 'render') && (
        <PageCarousel
          pages={session.artifacts.pages?.pages || []}
          onReorder={(newPages) => patchArtifact(sessionId, 'pages', { pages: newPages })}
          renderPageContent={(page) => (
            <PPTPageView
              page={page}
              sessionId={sessionId}
              showDesignText={view === 'design'}
              annotatable={session.currentStep === 'render'}
            />
          )}
        />
      )}
      
      <ExportButton sessionId={sessionId} format="pptx" />
    </PluginShell>
  )
}
```

**`PPTPageView` 组件**

- 展示单页内容（标题 + 正文设计文字 + 图片）
- 设计文字可直接编辑（与 `RichEditor` 集成）
- 图片区域支持 `ImageAnnotator`（render 步骤）
- 「重新渲染此页」按钮（触发 plugin_edit）

**导出 PPTX**

- `ExportButton` 触发 `GET /api/v1/plugin-sessions/:id/export?format=pptx`
- `backend/export.go` 使用 `go-pptx` 或类似库生成 PPTX 文件

### 1.6 `registry.ts` 注册

```ts
// frontend/src/modules/chat/plugins/registry.ts
import './writing-plugin'
import './ppt-plugin'   // 新增这一行
```

---

## 二、实施计划

### 阶段划分

**Week 1 — Scenario 文件 + plugin.yaml**

| 任务 | 负责层 | 工作量 |
|------|--------|--------|
| `plugin.yaml` 编写 | Algorithm | 0.5d |
| `scenario.md` / `state.yml` / `driver.md` 编写 | Algorithm | 1.5d |
| `prompts/outline.md` / `design.md` / `render.md` | Algorithm | 1.5d |
| validate 接口验证通过 | Algorithm | 0.5d |

**Week 2 — 算法层实现**

| 任务 | 负责层 | 工作量 |
|------|--------|--------|
| `_run_outline`（含大纲 JSON 格式） | Algorithm | 1d |
| `_run_design`（逐页 append_item） | Algorithm | 1.5d |
| `_run_render`（逐页调 generate_image + patch） | Algorithm | 1.5d |
| append_item 事件 Go Handler 扩展（若 M1 未完整实现） | Backend | 0.5d |

**Week 3 — 前端视图**

| 任务 | 负责层 | 工作量 |
|------|--------|--------|
| `frontend/types.ts` | Frontend | 0.5d |
| `PPTPageView` 组件（设计文字 + 图片 + 标注） | Frontend | 1.5d |
| PPT 主视图 `index.tsx` | Frontend | 1.5d |
| `ExportButton` PPTX 格式支持 | Frontend | 0.5d |
| `registry.ts` 注册 | Frontend | 0.5h |

**Week 4 — 后端导出 + 双插件联调**

| 任务 | 负责层 | 工作量 |
|------|--------|--------|
| `backend/export.go`（PPTX 导出） | Backend | 2d |
| 双插件并存场景联调 | All | 1d |
| 全流程 E2E 测试 | All | 1d |

---

## 三、测试方案

### 3.1 Scenario 文件校验

```bash
POST /api/v1/plugins/ppt-plugin/validate
期望：{ "is_valid": true, "errors": [] }
```

### 3.2 算法层单元测试

```python
def test_design_step_emits_append_item_per_page():
    # mock outline 包含 3 页
    # 验证 emit 了 3 个 append_item 事件，每个包含完整页面数据

def test_render_step_patches_image_url_per_page():
    # mock pages 包含 3 页（无 imageUrl）
    # mock generate_image 返回 URL
    # 验证 emit 了 3 个 patch(op='merge')，每个包含 imageUrl

def test_render_skips_already_rendered_pages():
    # 2 页中，1 页已有 imageUrl 且未指定修改
    # 验证只 emit 1 个 patch

def test_annotation_triggers_rerender():
    # ctx.payload.annotations 不为空，指定 page_id
    # 验证对应页面被重新渲染

def test_driver_md_advances_on_all_pages_rendered():
    # mock driver 判断：所有页面都有 imageUrl
    # 验证不调任何工具（流程结束）
```

### 3.3 前端组件测试

```ts
describe('PPTPlugin', () => {
  it('shows outline view in outline step', () => {
    // currentStep='outline'，验证 OutlineTree 可见，PageCarousel 不可见
  })
  it('shows page carousel in design/render step', () => {
    // currentStep='design'，验证 PageCarousel 可见
  })
  it('appends page to carousel on append_item event', () => {
    // dispatch append_item(item={id:'p1',...})
    // 验证 PageCarousel 多了一页
  })
  it('updates image url on patch merge event', () => {
    // dispatch patch(op='merge', path='/id=p1', value={imageUrl:'...'})
    // 验证 p1 页面的图片更新
  })
})

describe('PPTPageView', () => {
  it('shows design text in design step', () => {})
  it('shows image in render step', () => {})
  it('ImageAnnotator visible in render step', () => {})
  it('send annotation triggers plugin_edit', async () => {
    // 添加标注，发送
    // 验证 plugin_edit 请求包含 annotations 和 page_id
  })
})
```

### 3.4 双插件并存测试

```python
def test_two_different_plugins_in_same_conversation():
    # 创建 writing-plugin session（ps-001）
    # 创建 ppt-plugin session（ps-002）
    # 验证：两个 session 均创建成功
    # 验证：plugin_sessions 表有两条记录

def test_plugin_context_isolation():
    # ps-001（writing）活跃，ps-002（ppt）活跃
    # 对 ps-001 发送对话
    # 验证 agent context 中只有 writing 的 scenario.md，没有 ppt 的
    # 对 ps-002 发送对话
    # 验证 agent context 中只有 ppt 的 scenario.md，没有 writing 的
```

### 3.5 端到端（E2E）验收测试

**PPT 完整流程**

```
1. 对话框输入：「帮我做一个关于人工智能发展史的 PPT，共 8 页」
2. 大纲生成（8 页规划）
   → 验证 OutlineTree 显示 8 个页面节点
3. 确认大纲（点「继续」）
4. design 步骤（auto 模式）
   → 验证 PageCarousel 逐页出现（pages append_item）
   → 验证最终 8 页全部有设计文字
5. design 完成，agent 自动推进到 render
6. render 步骤
   → 验证每页图片逐渐加载
   → 验证 8 页全部有图片
7. 对第 3 页图片做标注「把背景改成蓝色渐变」，发送给 AI
   → 验证第 3 页图片更新（版本 +1）
   → 验证其他 7 页不变
8. 导出 PPTX
   → 验证 .pptx 文件可下载，内容包含 8 页
9. 刷新页面，验证所有内容保留
```

**双插件并存验收**

```
1. 打开写作插件（生成一篇文章大纲）
2. 在同一会话中触发 PPT 插件
3. 验证 PPT 插件正常启动（非同 plugin_id，不触发 409）
4. 两个插件均处于活跃状态
5. 对写作插件发送对话「修改第二章标题」
   → 验证修改的是写作插件的 outline，PPT 的 pages 不变
6. 对 PPT 插件发送对话「第 3 页设计文字太长了」
   → 验证修改的是 PPT 插件的 pages，写作的 outline 不变
7. 两个 VersionHistory 分别独立，互不干扰
```

---

## 四、验收标准

| 验收项 | 验收方式 | 通过标准 |
|--------|----------|----------|
| Scenario 校验通过 | validate API | is_valid=true |
| 逐页 append_item 实时展示 | E2E | PageCarousel 逐页出现 |
| render 步骤逐页生成图片 | E2E | 每页图片加载成功 |
| 图片标注触发单页重渲染 | E2E | 只有标注页图片变化 |
| auto 模式自动推进三步 | E2E | design/render 均自动完成 |
| PPTX 导出 | E2E | 文件可下载，内容完整 |
| 双插件并存 session 隔离 | 单元测试 + E2E | context 不串台 |
| 同 plugin_id 创建返回 409 | 单元测试 | 正确 Toast 提示 |
| 刷新后数据保留 | E2E | 全部页面保留 |
| 写作插件不受影响 | E2E | M5 验收项仍全部通过 |

---

## 五、注意事项与风险

1. **`append_item` 的 Go Handler 实现**：M1 定义了 `append_item` 类型但 M1 可能未完整实现 Go 侧的追加逻辑（M1 重点是单步图片），M6 开始前需确认 `append_item` 在 Go 侧能正确追加到 artifact 的 JSON 数组。
2. **render 步骤并发生成**：8 页图片串行生成较慢（每页约 3-5s），建议改为并发生成（`asyncio.gather`），并注意并发写 artifact 的安全性（按 page_id 做精确 patch，而非整体替换）。
3. **PPTX 导出库**：Go 生态中 PPTX 生成库选项较少（`go-pptx`、`unioffice`），需要提前评估是否满足设计文字 + 图片的布局需求；若不满足，考虑 Python 侧用 `python-pptx` 实现导出并通过 Go 转发。
4. **图片 URL 时效性**：`generate_image` 返回的 URL 可能有时效（如 OpenAI DALL-E 生成的 URL 1 小时内有效），导出前需要将图片先下传到 OSS 持久化，否则导出 PPTX 中的图片可能失效。
5. **设计文字与图片的版本一致性**：用户修改设计文字后重新渲染，新图片应以新文字版本为准；需确保 `render` 步骤读取的是最新的 `pages` artifact，不能读缓存。
6. **M6 完成即验证框架完整性**：M6 通过后，意味着框架支持「新增插件只需关注内容侧」的目标已实现，可以作为框架成熟度的重要里程碑。
