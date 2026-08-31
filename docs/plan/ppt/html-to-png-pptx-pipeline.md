# HTML → PNG → PPTX 导出方案

> 目标：基于 **pptx skill** 生成的 HTML 幻灯片，在前端一键导出 PNG，并由后端/算法侧拼接为 PPTX。HTML 原文件与 PNG 预览一并保留。
>
> 补充产品计划（Composite 缩略图布局、可编辑/不可编辑模式选型）见：
> [ppt-plugin-composite-ux-and-modes.md](./ppt-plugin-composite-ux-and-modes.md)

## 总体流程

```text
pptx skill 生成 deck
  → pages/page_001.html ... page_NNN.html
  → 前端 [导出 PPT]：html-to-image 按页截图
  → Go core：鉴权、落盘 preview/*.png
  → Python chat：python-pptx 拼接 deck.pptx
  → 返回下载链接
```

## 架构

```text
┌─────────────┐     ① html-to-image      ┌──────────────┐
│  前端 React  │ ──────────────────────► │  PNG blobs   │
│  [导出 PPT]  │     (浏览器内 .wrapper)  │  page_001..N │
└──────┬──────┘                          └──────┬───────┘
       │ ② multipart/form-data                     │
       ▼                                           ▼
┌─────────────┐     ③ 转发/落盘          ┌──────────────┐
│  Go core    │ ──────────────────────► │ deck_dir/    │
│  POST /ppt  │                         │ preview/*.png│
└──────┬──────┘                          └──────┬───────┘
       │ ④ 调用算法侧                            │
       ▼                                           ▼
┌─────────────┐     ⑤ python-pptx        ┌──────────────┐
│ Python chat │ ──────────────────────► │  deck.pptx   │
│ build_pptx  │                         │  + 下载 URL  │
└─────────────┘                          └──────────────┘
```

### 职责划分

| 层 | 职责 |
|----|------|
| **pptx skill** | 生成 `task_pack` / `outline` / `pages/page_*.html` |
| **前端** | 加载 HTML、按页截图、上传 PNG、展示进度与下载 |
| **Go core** | 鉴权、deck 路径校验、PNG 落盘、调用 Python、返回 signed URL |
| **Python chat** | 读取 `preview/*.png`，按页序拼接 `deck.pptx` |

---

## Deck 目录约定

pptx skill 产出目录结构：

```text
ppt_decks/{deck_id}/
  task_pack.json
  outline.json
  style_spec.json
  pages/
    page_001.html
    page_002.html
    ...
  preview/              # 前端导出后写入
    page_001.png
    page_002.png
    ...
  {deck_id}.pptx        # 算法侧拼接产物
```

---

## 前端设计

### 入口

在 PPT 预览页或 chat artifact 预览区提供：

```text
[导出 PNG]  [导出 PPT]
```

- **导出 PNG**：浏览器内 `html-to-image` 截图，支持逐页下载或打包下载
- **导出 PPT**：截图后上传 PNG，调用后端拼接 PPTX

### 技术选型

- 库：`html-to-image`
- 截图目标：`.wrapper`（1600×900，`pixelRatio: 2`）
- 渲染方式：隐藏 `iframe` 逐页加载 deck 内 HTML

### 核心接口（前端）

```ts
async function exportDeckToPng(deckId: string, pageNames: string[]) {
  const blobs: Blob[] = [];
  for (const name of pageNames) {
    const blob = await renderPageToPng(`/decks/${deckId}/pages/${name}`);
    blobs.push(blob);
  }
  return blobs;
}

async function exportDeckToPptx(deckId: string, blobs: Blob[]) {
  const form = new FormData();
  form.append('deck_id', deckId);
  blobs.forEach((b, i) => form.append('pages', b, `page_${String(i + 1).padStart(3, '0')}.png`));
  const res = await fetch('/api/core/ppt/decks:export', { method: 'POST', body: form });
  return res.json();
}
```

### 渲染约定

- HTML 页面使用固定画布 `.wrapper`（16:9）
- 含 ECharts 的页面：等待图表 ready 标记后再截图
- HTML 与静态资源通过同域路由提供，避免跨域截图失败

---

## Go core 设计

### API

```http
POST /api/core/ppt/decks:export
Authorization: Bearer ...
Content-Type: multipart/form-data

deck_id={deck_id}
pages=@page_001.png
pages=@page_002.png
...
```

响应：

```json
{
  "deck_id": "my_deck_20260722_120000",
  "pptx_url": "https://.../signed-url/my_deck.pptx",
  "page_count": 4,
  "preview_dir": ".../preview/",
  "html_dir": ".../pages/"
}
```

### 处理流程

1. RBAC：校验用户对 deck / conversation / artifact 的访问权限
2. 校验：`page_count`、单文件与总大小上限（建议单页 ≤ 2MB，总计 ≤ 20MB）
3. 落盘：`{upload_root}/ppt_decks/{deck_id}/preview/page_NNN.png`
4. 调用 Python：`POST http://chat:8046/api/ppt/build`
5. 返回 `pptx` 的 signed download URL

### 设计原则

- 统一走 Kong + RBAC 鉴权
- 统一使用 `LAZYMIND_UPLOAD_ROOT` 落盘
- 与 chat / artifact 生命周期保持一致

---

## Python chat 设计

### API

```http
POST /api/ppt/build
Content-Type: application/json

{
  "deck_dir": "/var/lib/lazymind/uploads/ppt_decks/my_deck_20260722_120000",
  "deck_id": "my_deck_20260722_120000",
  "output": "/var/lib/lazymind/uploads/ppt_decks/my_deck_20260722_120000/my_deck.pptx"
}
```

响应：

```json
{
  "status": "ok",
  "deck_id": "my_deck_20260722_120000",
  "output": ".../my_deck.pptx",
  "page_count": 4,
  "included_pages": [1, 2, 3, 4]
}
```

### 拼接逻辑

```python
# algorithm/lazymind/chat/engine/ppt/build_pptx.py
from pptx import Presentation
from pptx.util import Inches

SLIDE_W = Inches(13.333)  # 16:9
SLIDE_H = Inches(7.5)

def build_pptx_from_pngs(deck_dir: Path, output: Path) -> dict:
    # 1. 读取 outline.json 确定页序；若无则按 page_001..page_NNN 排序
    # 2. 读取 preview/page_NNN.png
    # 3. 每页创建空白 slide，满版贴图
    # 4. 保存 output
    ...
```

页序规则：

- 优先读 `outline.json` 的 `page_no`
- 否则按 `page_001.png`、`page_002.png` 字典序

依赖：`python-pptx`

---

## 与 pptx skill 的衔接

```text
用户触发 pptx skill
  → 生成 HTML pages
  → 前端展示预览
  → 用户点击 [导出 PPT]
  → 前端截图 PNG
  → 后端拼接 PPTX
  → artifact 回传 pptx_url + 保留 html/png
```

导出完成后，chat 侧可追加 artifact：

```json
{
  "type": "pptx",
  "deck_id": "my_deck_20260722_120000",
  "pptx_url": "...",
  "page_count": 4,
  "html_pages": ["page_001.html", "page_002.html"],
  "preview_pages": ["page_001.png", "page_002.png"]
}
```

---

## 分阶段实施

### Phase 1 — MVP

1. 前端：抽取 `html-to-png-tool` 为 `frontend/src/modules/ppt/export/`
2. Python：实现 `build_pptx_from_pngs()` + `/api/ppt/build`
3. Go core：实现 `POST /ppt/decks:export`
4. UI：PPT 预览页增加「导出 PPT」按钮

验收：点击一次按钮，获得可打开的 PPTX，且 `pages/` 与 `preview/` 均保留。

### Phase 2 — 产品化

1. pptx skill 生成完成后自动挂载预览与导出入口
2. deck 与 `conversation_id` / `task_id` 绑定
3. SSE 进度：`export_started` → `png_uploaded` → `pptx_ready`
4. 缺页、上传失败、图表未渲染等错误提示

### Phase 3 — 增强

1. 前端 Web Worker 并行截图
2. 大 deck（>10 页）走后端异步任务
3. 支持仅导出 PNG、仅导出 PPTX、PNG+PPTX 打包下载

---

## 目录结构（落地后）

```text
frontend/src/modules/ppt/
  export/
    renderPageToPng.ts
    uploadDeckPngs.ts
    ExportPptButton.tsx

backend/core/ppt/
  handler.go
  service.go

algorithm/lazymind/chat/
  api/ppt_routes.py
  engine/ppt/build_pptx.py

html-to-png-tool/          # 本地调试工具（可保留）
```

---

## 依赖清单

| 组件 | 依赖 | 说明 |
|------|------|------|
| 前端截图 | `html-to-image` | 浏览器内 DOM → PNG |
| 后端拼接 | `python-pptx` | PNG 满版贴入 16:9 slide |
| 存储 | Go core upload | 已有 `LAZYMIND_UPLOAD_ROOT` |

---

## 验收标准

1. pptx skill 生成 N 页 HTML 后，前端可逐页预览
2. 点击「导出 PPT」后，生成 N 页可打开的 PPTX
3. `pages/*.html` 与 `preview/*.png` 原样保留
4. 导出链路经 Go core 鉴权，返回 signed URL
5. 页序与 `outline.json` 一致
