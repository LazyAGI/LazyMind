# 命名演示素材库

工作流自带 318 个图标、12 个系列，约 7.6 MiB。HTML 使用 SVG 或透明 PNG，原生 PPTX 使用
预渲染 PNG，保持原始比例。素材不依赖 CDN，不需要模型手画图标，也不代替真实证据图片。

打开 [素材浏览器](gallery.html)，按中文含义、英文名、系列检索。卡片上显示可直接使用的稳定名称。

| 系列 | 方向 | 官方来源 | 许可 |
| --- | --- | --- | --- |
| lucide | 线性 | https://lucide.dev | ISC / Feather MIT |
| tabler | 线性 | https://github.com/tabler/tabler-icons | MIT |
| ph | 双色 | https://github.com/phosphor-icons/core | MIT |
| fluent | 线性 | https://github.com/microsoft/fluentui-system-icons | MIT |
| fluent-color | 彩色 | https://github.com/microsoft/fluentui-system-icons | MIT |
| material-symbols | 线性 | https://github.com/google/material-design-icons | Apache-2.0 |
| carbon | 线性 | https://github.com/carbon-design-system/carbon | Apache-2.0 |
| bi | 线性 | https://github.com/twbs/icons | MIT |
| heroicons | 线性 | https://github.com/tailwindlabs/heroicons | MIT |
| iconoir | 线性 | https://github.com/iconoir-icons/iconoir | MIT |
| fluent-emoji-flat | 彩色 | https://github.com/microsoft/fluentui-emoji | MIT |
| fluent-3d | 透明 3D | https://github.com/microsoft/fluentui-emoji | MIT |

## 工作流用法

1. 素材收集阶段调用 `ppt_search_assets(query="需求分析", collection="fluent-3d")`。
2. 将选中名称、用途记录在 material_summary；需要本地文件时调用
   `ppt_get_asset(name="fluent-3d:clipboard")`。返回 HTML / PPTX 文件路径、尺寸、来源和许可。
3. HTML 生成时插入以下标签，生成器在发布前解析为真实本地路径：

```html
<img data-ppt-asset="fluent-3d:clipboard" alt="需求分析"
     style="width:100px;height:100px;object-fit:contain">
```

4. 原生 `ppt_fill_template` 的页面配置支持：

```json
{"source_page":5,"icons":[{"name":"fluent-3d:clipboard","box":[1,2,1,1]}]}
```

box 单位为英寸。每页最多 8 个原生图标或 16 个 HTML 图标。图标与证据材料的索引独立；
文字标签仍然使用文字对象。不存在的名称明确报错，不能猜测名称或悄悄替换。

## HTML → PDF 验收标准

参考用户认可的展示稿组织层级，不固定所有稿件的配色或风格：

- 先用一句结论，再用流程、并列关系或对照图表达方法；保留事实边界，避免复制长段落。
- 同页选同一家族图标，保持光照、色调和尺寸一致。彩色 / 3D 图标配浅渐变台座和柔和阴影。
- 标题、正文、标签、公式保留自然字形；禁止 scaleX、非等比缩放、SVG textLength 拉宽文字。
- SVG 使用正确 viewBox 和等比缩放，图片用 contain，连线避开标签。空间不足时重新排版。
- 固定 1600 × 900 画布，保留安全边距；图像不能遮挡文字。预览与 PDF 使用同一 HTML。
- 导出器等待字体、图片，检查真实 DOM 的文字变形、画布越界、图片拉伸和加载失败。
  检查失败时停止导出并返回元素定位，保留旧 PDF。该检查不代替内容审校、重叠检查或视觉审稿。
- 可编辑 PPTX 的渐变、阴影和复杂图形仍需单独验证；HTML / PDF 通过不代表原生对象完全一致。

[参考页面](visual-standard.html) 是带命名占位符的生成示例。先用
`runtime/lib/presentation_assets.resolve_html` 解析，保存为 deck/pages/page_001.html，
再由既有 html_to_pdf.mjs 导出。不要把整页示例当图片嵌入。

## 来源与维护

SVG 来自 [Iconify 开源集合](https://github.com/iconify/icon-sets)，3D 来自 Microsoft Fluent Emoji。
`catalog.json` 固定来源提交、完整许可文件、尺寸、中文关键词和每个文件的 SHA-256。
294 个 SVG 同时带 512px PNG，24 个 3D 图标保留上游透明 PNG。完整许可保存在 licenses/；
每次素材落盘时同时生成 ASSET-LICENSES.txt，分发素材时保留该文件。

更新时使用明确的上游提交：校验 SVG 不含脚本、外部资源或 foreignObject，保留 viewBox，
添加 preserveAspectRatio="xMidYMid meet"；将 currentColor 固定为实际色值；透明背景等比
渲染 PNG；更新文件哈希、许可和预览。不要把整个上游集合拉进工作流。
