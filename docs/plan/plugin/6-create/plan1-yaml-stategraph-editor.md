# 计划 1：YAML 校验 + 状态机图形化双向编辑器

> 聚焦核心编辑体验：YAML 合法性校验 + 图形画布与 YAML 文本的双向同步编辑。
> 不涉及版本管理、发布、系统热重载，也不涉及 Skill 生成入口。

---

## 背景与范围

本计划覆盖 target.md 中以下条目：

- §三（条目 7–11）：状态机合法性校验
- §四（条目 12–14）：图形化编辑
- §五（条目 15–16）：YAML 双向编辑

不覆盖：§一（权限）、§二（接口持久化）、§六（版本管理）、§七（Skill 生成）——这些归属计划 2。

---

## UI 参考

`docs/plan/plugin/6-create/image.png`

图中展示了画布主区域。交互设计如下：
- 右上角切换按钮：**"画布 / YAML"** 两种视图（类似 Cursor 的 Visual/Source 切换）
- YAML 视图展示时，隐藏 `x-layout` 布局字段，只呈现语义内容
- 选中节点时，右侧滑出属性面板（不遮挡画布主区，类似侧边抽屉）

---

## 一、展示与编辑分离原则

**展示（StateGraphModal/StateGraphView）和编辑（StateGraphEditor）是两个独立组件，不共享实现，只共享 UI 视觉风格。**

| 维度 | 展示组件（现有）| 编辑组件（本计划新建）|
|------|----------------|----------------------|
| 技术方案 | dagre + 手写 SVG | React Flow（见下节权衡）|
| 核心关注 | 执行状态渲染（颜色/进度/Popover）| 拖拽体验（节点增删、连线、属性编辑）|
| 数据来源 | 服务端下发的运行时快照 | 本地 GraphModel（从 YAML 解析）|
| 交互 | 只读，点击查看详情 | 可编辑，拖拽/连线/属性面板 |
| 改动 | 不动 | 全新实现 |

视觉风格对齐：节点卡片外观、圆形终端节点、边箭头样式与现有 StateGraphView 保持一致，用户在展示和编辑之间切换时感知连贯。

---

## 二、技术选型

### 2.1 编辑画布：React Flow

**选型理由：**

| 对比项 | 继续用 dagre+SVG 手写 | 引入 React Flow |
|--------|----------------------|-----------------|
| 拖拽节点 | 需手写 mousedown/mousemove/mouseup，处理 SVG 坐标变换、边实时跟随 | 内置，开箱即用 |
| 节点连线 | 需手写 handle 命中检测、连线预览、锚点计算 | 内置 handle + edge 系统 |
| 选中/多选 | 需手写框选逻辑 | 内置 |
| 布局保存 | 自行维护坐标 | `node.position` 即坐标，直接持久化 |
| 自定义节点 | 完全自由（SVG foreignObject）| 自定义 React 组件，更灵活 |
| 维护成本 | 高（拖拽 edge-case 很多）| 低（社区活跃，MIT）|
| 包体积 | 0（已有 dagre）| ~180KB gzip 后 ~50KB，**按需懒加载**，不影响首屏 |

结论：**编辑组件使用 React Flow**，展示组件保持现有 dagre+SVG 不变。

包名：`@xyflow/react`（React Flow v12，MIT 协议）。

### 2.2 YAML 文本编辑器：Monaco Editor

项目目前无 Monaco，需新增。Monaco 是目前最成熟的浏览器代码编辑器，支持：
- YAML 语法高亮
- 错误 squiggle（通过 `editor.setModelMarkers` 注入）
- 行内 hover tooltip

**包体积问题**：Monaco 完整包约 2MB+，必须懒加载。

### 2.3 YAML 解析：js-yaml

`js-yaml`（MIT，3KB gzip）是 Node.js/浏览器通用的 YAML 解析库，项目中已有间接依赖（通过后端工具链），前端直接 `import` 即可。

### 2.4 按需懒加载策略

两个重量级依赖（React Flow + Monaco）只在用户实际进入编辑器时加载，不影响首屏。

**实现方式：Vite 动态 `import()` + `React.lazy`**

项目已有先例：`MermaidBlock.tsx` 用动态 `import()` 延迟加载 mermaid。本方案沿用同一模式。

```
StateGraphEditor（入口）
  └─ React.lazy(() => import('./GraphCanvas'))   ← 包含 @xyflow/react
  └─ React.lazy(() => import('./YamlEditor'))    ← 包含 monaco-editor
```

Vite 会自动将这两个 lazy chunk 打成独立的 JS 文件，只有在对应视图渲染时才触发网络请求。

---

## 三、存储方案

### 核心原则：以 YAML 为唯一 Source of Truth

| 维度 | 方案 | 理由 |
|------|------|------|
| 持久化存储 | 只存 YAML 文本（`state.yml`）| YAML 已包含完整语义，无需额外存图结构 |
| 节点布局（画布坐标）| 存入 YAML 的扩展字段 `x-layout` | 与状态机定义合并，避免两张表同步问题 |
| 前端内存状态 | `GraphModel`（节点 + 边 + 布局）| YAML parse/serialize 的中间表示 |
| 不单独存图结构 | 不存 adjacency list / JSON graph | 图结构可随时从 YAML 派生 |

**YAML 扩展字段示例**（`x-layout` 前缀符合 YAML 扩展约定，driver 读取时忽略未知字段）：

```yaml
x-layout:
  step_collect_info: { x: 120, y: 80 }
  step_review:       { x: 320, y: 80 }
  step_generate:     { x: 520, y: 200 }

slots:
  doc_draft:        { type: file }
  review_comments:  { type: text }

steps:
  - id: step_collect_info
    label: 收集信息
    mode: human
    outputs: [doc_draft]
    transitions:
      - to: step_review
        condition: 信息收集完毕

  - id: step_review
    label: 审阅
    mode: human
    inputs: [doc_draft]
    outputs: [review_comments]
    transitions:
      - to: step_generate
        condition: 审阅通过
      - to: step_collect_info
        condition: 需要补充信息

  - id: step_generate
    label: 生成结果
    mode: auto
    inputs: [doc_draft, review_comments]
    outputs: []
    transitions:
      - to: __end__
        condition: always
```

### YAML ↔ GraphModel 转换层

```
YAML text
  └─ parse()      ──► GraphModel { nodes[], edges[], layout{}, slots{} }
                          └─ serialize() ──► YAML text
```

双向转换均为纯函数，无副作用，便于单元测试。详见 `code.md`。

---

## 四、YAML 校验

### 4.1 校验时机

| 触发点 | 行为 |
|--------|------|
| YAML 视图编辑（debounce 500ms）| 语法校验 + 结构校验，squiggle 标注错误行 |
| 图形视图每次操作完成 | 结构校验，问题节点/边红色描边 |
| 点击"保存 Draft" | 完整校验，失败展示错误列表，不关闭编辑器 |

### 4.2 校验规则（对应 target §三）

| 编号 | 规则 | 对应条目 |
|------|------|---------|
| V1 | 必须存在 `__start__` 和 `__end__` 节点 | 条目 7 |
| V2 | 不允许孤立节点（无输入且无输出，`__start__` 除外）| 条目 7 |
| V3 | 不允许有向环（DFS 检测）| 条目 7 |
| V4 | 每个节点至少一条输出边（`__end__` 除外）| 条目 8 |
| V5 | 除 `__start__` 外每个节点至少一条输入边 | 条目 8 |
| V6 | 输出边必须指定 `slot` | 条目 8 |
| V7 | 输入引用的 `slot` 必须由拓扑前序节点产出 | 条目 8 |
| V8 | `slot` 类型与插件级集中定义一致 | 条目 9 |
| V9 | 转移边 `condition` 字段不得为空 | 条目 10 |
| V10 | YAML 语法合法（能被解析器解析）| 基础前提 |

### 4.3 校验结果展示

- **YAML 视图**：错误行红色 squiggle + hover tooltip
- **图形视图**：问题节点/边红色描边，hover 显示错误信息
- **底部错误面板**：汇总所有错误，点击跳转到对应节点/行

### 4.4 实现位置

校验逻辑封装为纯函数 `validateStateGraph(model: GraphModel): ValidationError[]`，前端实时调用。后端保存接口收到 YAML 后用 Go 实现同一套规则做二次兜底（防止绕过前端直接调接口）。

---

## 五、图形化编辑（React Flow）

### 5.1 自定义节点

React Flow 支持完全自定义节点组件。编辑态节点卡片在视觉上对齐现有 `StateGraphView` 的节点样式，但：
- 不显示执行状态（无状态色点、无执行次数）
- 显示 mode 标签（human / auto）
- 节点右键菜单：编辑属性 / 删除
- 选中时边框高亮，右侧属性面板滑出

### 5.2 节点编辑（对应 target 条目 12）

| 操作 | 交互方式 |
|------|----------|
| 新增节点 |  画布空白处右键菜单 / 鼠标在已有节点时，边缘出现+号，点击就新建一个新节点，自动连线上去 |
| 删除节点 | 选中后 `Delete` 键 / 右键菜单 |
| 编辑步骤 ID | 属性面板 → id 字段（唯一性实时校验）|
| 编辑显示标签 | 属性面板 → label 字段 |
| 编辑 human/auto 模式 | 属性面板 → mode 切换 |
| 配置输出 slot | 属性面板 → 从 slots 列表选择或新建 |
| 配置输入 slot | 属性面板 → 仅列出拓扑前序节点产出的 slot |

### 5.3 边编辑（对应 target 条目 13）

| 操作 | 交互方式 |
|------|----------|
| 新增转移边 | 拖拽节点 handle → 目标节点（React Flow 内置）|
| 删除边 | 选中后 `Delete` 键 / 右键菜单 |
| 编辑转移条件 | 边中间点击弹出内联输入框（自定义 EdgeLabel 组件）|

### 5.4 布局持久化（对应 target 条目 14）

- React Flow 的 `node.position` 即 `{x, y}`，拖拽结束 `onNodeDragStop` 回调直接将位置写入 `GraphModel.layout`
- `GraphModel` 变更触发 serialize → 更新 YAML 视图
- 保存 Draft 时布局信息通过 `x-layout` 字段持久化进 YAML

---

## 六、YAML 双向编辑

### 6.1 同步机制（对应 target 条目 15–16）

```
图形视图操作（增删节点/边、拖拽、属性编辑）
  └─► dispatch action → GraphModel 更新
        └─► serialize(GraphModel) → YAML string
              └─► 调用 Monaco editor.setValue()（静默更新，不触发 onChange）

YAML 视图编辑（debounce 500ms）
  └─► onChange(yamlString)
        ├─► 语法校验（js-yaml.load）
        │     ├─ 成功 → parse(yaml) → 新 GraphModel → React Flow setNodes/setEdges
        │     └─ 失败 → 保留上一有效 GraphModel + Monaco squiggle 标红
        └─► 结构校验（validateStateGraph）→ 更新错误面板
```

两侧共享 `useRef<GraphModel>` 作为唯一内存状态，视图切换时无数据丢失。

### 6.2 视图切换

右上角 SegmentedControl（Ant Design `Segmented`）：
- **画布** → 显示 React Flow 画布，隐藏 Monaco
- **YAML** → 显示 Monaco 编辑器，YAML 内容不含 `x-layout` 字段（布局信息对用户隐藏，切回画布时恢复）

`x-layout` 处理：
- 画布 → YAML：serialize 时剥离 `x-layout`，Monaco 展示纯语义 YAML
- YAML → 画布：parse 时合并当前 `GraphModel.layout`（用户在 YAML 中不能/不需要手写坐标）

### 6.3 边界情况

| 情况 | 处理 |
|------|------|
| YAML 语法错误 | 图形视图保持上一有效状态，不重绘 |
| 图操作产生 YAML 格式差异 | canonical serializer 保证 key 顺序和缩进固定 |
| 切换视图时有未保存修改 | 两侧共享同一 `GraphModel`，不丢失 |

---

## 七、前端

### 入口

在“智积阅累/技能”模块加一个插件管理，和“我的技能”，“技能市场”并列，加一个“我的插件”。允许用户新建插件，用户新建插件的时候，给一个空白面板；yaml可以按照我们的 存储方案 进行存储，先不考虑版本

### 组件结构

```
src/modules/plugin/components/StateGraphEditor/
├── index.tsx                   # 入口：GraphModel state + 视图切换 + 工具栏
├── GraphCanvas/
│   ├── index.tsx               # React.lazy 包装，Suspense fallback
│   ├── Canvas.tsx              # React Flow 画布（实际内容，懒加载 chunk）
│   ├── StepNode.tsx            # 自定义节点（对齐 StateGraphView 视觉）
│   ├── TransitionEdge.tsx      # 自定义边（含内联条件编辑）
│   └── NodePropertiesPanel.tsx # 右侧属性面板
├── YamlEditor/
│   ├── index.tsx               # React.lazy 包装，Suspense fallback
│   └── Editor.tsx              # Monaco 实例（懒加载 chunk）
├── ValidationPanel/
│   └── index.tsx               # 底部错误列表
└── core/
    ├── model.ts                # GraphModel 类型定义
    ├── parser.ts               # YAML string → GraphModel
    ├── serializer.ts           # GraphModel → YAML string
    └── validator.ts            # ValidationError[]，规则 V1–V10
```

懒加载边界：
- `GraphCanvas/Canvas.tsx` 包含 `import { ReactFlow } from '@xyflow/react'`，是一个独立 Vite chunk
- `YamlEditor/Editor.tsx` 包含 `import * as monaco from 'monaco-editor'`，是另一个独立 chunk
- 两个 `index.tsx` 只做 `React.lazy` + `Suspense` 包装，不引入任何重量级依赖

---

## 八、交付物

| 交付物 | 说明 |
|--------|------|
| `StateGraphEditor` 前端组件 | 可独立嵌入 Plugin Detail 页 |
| `core/validator.ts` + 单元测试 | 10 条校验规则 |
| `core/parser.ts` + `core/serializer.ts` + 单元测试 | YAML ↔ GraphModel 双向转换 |
| 后端 Draft 保存接口（校验兜底）| `POST /plugins/:id/draft`，接收 YAML，Go 侧同一套规则二次校验后存库 |
| DB 表 `plugin_drafts` | `plugin_id`, `content`(YAML text), `updated_at`, `updated_by` |

---

## 九、不在本计划范围内

- 版本号管理、发布接口、热重载 → 计划 2
- 权限控制（管理员限定）→ 计划 2
- Skill → 插件草稿生成 → 计划 2
- scenario 完整展示（plugin.yaml / scenario.md / driver.md）→ 计划 2
边缘- 系统如何加载并使用动态创建的插件 → 计划 2
