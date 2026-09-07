# PPT 底图 Prompt

底图是后续文字、图表和素材的承载层，不是带字海报。标题、正文、数字、Logo、图表、UI 卡片和页码默认由 PPT/HTML 排版层生成。

## 必备合同

每条底图 Prompt 都必须独立包含：

1. `16:9 widescreen presentation background` 和背景用途。
2. 与当前页面真实布局匹配的安全区位置与大致比例。
3. 视觉焦点及其所在区域；细节、纹理与高对比不能穿过文字安全区。
4. 具体媒介、材质、光线与色板。
5. 系列锚点和本页独有的叙事变化。
6. 简短禁项：无文字、字母、数字、Logo、水印、UI、图表、标签和占位框。

不要只写 `leave space for text`；说明留在哪里、留多少、如何保持可读：低纹理、低对比、连续色块或柔和渐变，并避免在安全区放人脸、地平线切线、强光斑和复杂轮廓。

## 布局映射

- 左文右图：左侧约 40–45% 为连续、低纹理、低对比安全区；主体和高频细节集中在右侧 45–55%，主体视线可朝向文本。
- 左图右文：镜像上述规则。
- 中央标题/封面：中央约 55–65% 保持安静，母题可作为边框、远景、上下弧线或四角聚拢；不要把强焦点压在标题后。
- 顶部标题 + 下方内容：顶部约 20–25% 平静；叙事性视觉放在下半部或两侧，避免形成一整块难以覆盖的中心焦点。
- 数据/图表页：大部分画布保持均匀、低对比；只在边缘或角落保留弱装饰。不要让生图模型伪造数据、坐标、表格或仪表盘。
- 章节过渡：允许更强的全幅母题，但仍给标题所在区域清晰的明暗分离和足够静区。
- 结束页：以收束性的远景、地平线、光晕、弧线或单一物体形成结束感，并保留 CTA/结语区域。

若上游尚未提供每页布局，默认左侧 42% 为文字安全区、右侧为视觉焦点；整套中不要无理由左右随机切换。

## Prompt 骨架

```text
A 16:9 widescreen presentation background for [page role/topic].
[Exact safe-zone location, proportion, low-texture and low-contrast behavior].
[Page-specific visual metaphor] concentrated in [visual region], with [foreground/midground/background relationship].
[Medium/camera grammar]. Materials: [...]. Lighting: [...]. Palette: [...] with accents confined to [...].
Series anchor: [the exact repeated deck-wide visual DNA].
Background only, without words, letters, numbers, logos, watermarks, UI, charts, labels, frames, or text placeholders.
```

## 防止模板化退化

- 不默认使用蓝紫发光、玻璃拟态、漂浮数据粒子、随机几何拼贴或整页黑底；只有主题/风格明确要求时才使用。
- 视觉隐喻必须与本页内容有关，例如供应链可用有方向的层级流动，组织协作可用汇聚/连接，增长可用尺度和空间展开；不要把所有主题都画成神经网络。
- 底图不能同时承担信息图职责。精确流程、比较、组织结构、时间线和数据图应由 SVG、ECharts 或 PPT 形状完成。
