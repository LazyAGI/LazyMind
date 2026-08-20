# PPT 插件：Composite UX 与导出模式选型

> 状态：§一 Composite UX **已落地**；§二 已切换为 **sn-ppt HTML + 可编辑 PPTX**（JSON 模板路径废弃）  
> 相关：生成由 `plugins/ppt-plugin/scripts/tools.py` 包装 `skills/sn_ppt/sn-ppt-standard`；预览为 HTML；导出优先 `pptx_output` 文件产物。

## 背景与问题

1. **Composite 布局不适合 PPT 阅读习惯**  
   当前左侧是页码 bar，讲稿（`preview_notes`）与幻灯片并排。更符合演示工具的预期是：左侧缩略图导航、主区大图预览、讲稿在幻灯片下方。

2. **导出路径「好看」与「可编辑/快」互相打架**  
   - 自由 HTML / 高保真截图：视觉好，但依赖重、可编辑性差或字体易乱码。  
   - JSON 固定模板：导出快、文字原生可编辑，但版式单一、色调有限。  
   产品上应在**开始生成前**让用户明确选择，而不是事后发现对不上预期。

---

## 一、前端 Composite 布局改造（已落地）

### 目标布局

```text
┌──────────┬─────────────────────────────┐
│ 缩略图   │                             │
│ 列表     │      幻灯片主预览区          │
│ (可拖拽) │      (preview_html / JSON)  │
│          │                             │
│          ├─────────────────────────────┤
│          │  讲稿文本框（preview_notes） │
│          │  放在幻灯片正下方            │
└──────────┴─────────────────────────────┘
```

### 交互细则

| 项 | 行为 |
|----|------|
| 左侧导航 | 由页码数字 bar 改为**缩略图列表**（可先用缩放后的 slide 预览图 / 占位骨架） |
| 拖拽调序 | 拖动缩略图时，插入位置用**线条指示器**（与现有 `SortableImageList` 的 insert gap 一致），松手后重排 `preview_html` + `preview_notes` 的 `sort_order` |
| 讲稿位置 | `preview_notes` 从右侧栏改到**主预览下方的文本框**；支持只读展示 + 后续可编辑 |
| 导出入口 | 仍挂在 composite 工具条（如「导出 PPTX」），与缩略图区并存 |

### 配置约定（plugin.yaml）

- 仅 PPT 的 `composite_tab_position` / 新字段（如 `composite_nav: thumbnails`）启用该布局。  
- 图片插件等其它 `layout: composite` **不**套用缩略图 bar（保持现有「无 page bar 的 plain composite」）。

### 代码同步范围（实施时）

- `PluginPanel/index.tsx`：`CompositeSlotGrid` / page bar → thumbnail rail + line drop indicator  
- `PluginPanel.scss`：缩略图列、主区、底部讲稿区样式  
- `plugins/ppt-plugin/plugin.yaml`：composite 布局声明与 slot 权重（主区 slide、下方 notes）  
- 必要时 `plugin-format.md` / `docs/plan/plugin/03-data_history` 补充 composite 文档

### 验收

1. 左侧为缩略图，点击切换当前页  
2. 拖拽时出现线条指示器，松手后页序与讲稿对齐不变乱  
3. 讲稿只出现在幻灯片下方，不再占右侧大栏  
4. 图片插件结果页无缩略图 bar 回归

---

## 二、生成前模式选择（产品流程，先写 plan）

### 入口时机

用户触发 PPT 插件 / `analyze_requirements` **开始前或第一步**，用明确选择题（或确认卡片）询问：

> 你希望最终 PPT 是？  
> A. **可编辑文字**  
> B. **不可编辑（高保真整页图）**

若选 A，再二次确认子模式。

### 模式矩阵

| 模式 | 用户表述 | 生成侧 | 导出侧 | 依赖 | 观感 | 速度 |
|------|----------|--------|--------|------|------|------|
| **A1 可编辑 · 高保真** | 「好看且可改字」 | HTML（或 bg+text 分层）质量优先 | 背景/整页需截图或 Playwright 渲染后再叠原生文字，或 DOM→shape | **需下载/安装依赖**（如 Playwright / Chromium、本地字体校验） | 高 | 慢 |
| **A2 可编辑 · 模板** | 「能改字、导出快」 | **JSON SSOT → 固定模板**（当前主路径） | pptxgenjs 原生文本框，系统字体 | 无额外浏览器依赖 | 色调/版式单一 | 快 |
| **B 不可编辑** | 「只要看起来和预览一样」 | HTML 或模板渲染均可 | 整页 PNG 贴入 PPTX（备注可仍可写 speaker notes） | 浏览器内 `html-to-image` 即可；服务端可选 Playwright | 与预览一致 | 中 |

> 说明：A1 的「好看」依赖渲染引擎与字体环境，**必须在 plan/产品文案中写明需安装 Playwright 等依赖**；未就绪时应降级提示到 A2 或 B，而不是静默失败。

### 推荐默认

- 默认推荐 **A2（JSON 模板可编辑）**：零额外依赖、导出稳定。  
- 用户明确要求「海报级 / 炫酷视觉」再引导 A1 或 B。

### 与现有实现的关系（本阶段不改代码）

| 模式 | 现状 |
|------|------|
| A2 | 已基本落地（slide JSON + 模板预览 + pptxgenjs + 备注） |
| B | 曾有 HTML→PNG 满版贴图方案，见 pipeline 文档 |
| A1 | 实验过 bg 截图 + 文字框，规则与窜行问题未闭环 |

本 plan **只定义产品分支与后续工作项**，不在本轮改 `generate_ppt` / 导出逻辑。

### 后续实现 checklist（供开工时勾选）

1. [ ] Chat / 插件冷启动增加模式选择 UI 或 `runtime_instruction` 问询  
2. [ ] `requirement_analysis` 写入 `export_mode: editable_template | editable_fidelity | raster`  
3. [ ] Driver / scenario 按 mode 分支（prompt、验收、导出按钮文案）  
4. [ ] A1：文档化 Playwright 安装与健康检查；失败时引导 A2/B  
5. [ ] Composite 缩略图 + 底部讲稿布局（见第一节）

---

## 三、里程碑建议

| 阶段 | 内容 | 代码 |
|------|------|------|
| M0 | 本文档对齐产品预期 | 仅 plan |
| M1 | Composite：缩略图 + 拖拽线条指示器 + 讲稿下置 | 前端 + plugin.yaml |
| M2 | 生成前模式问询（先只打标 `export_mode`，导出仍走 A2） | 插件 prompt / 轻量 UI |
| M3 | 按 mode 接通 B / A1 导出链路与依赖检测 | 导出与运维文档 |

---

## 四、非目标（本 plan 明确不做）

- 不在本轮修改运行时生成/导出代码逻辑  
- 不强制图片插件使用 PPT 缩略图 composite  
- 不承诺 A1 在无 Playwright 环境下可用
