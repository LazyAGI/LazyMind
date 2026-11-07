# HTML 模板信息与背景继承

`ppt_analyze_template` 保存 schema_version=2 的 template/template_profile.json。
其中 html_template 由 template_html.py 直接读取 PPTX OOXML，不把整页截图当背景。

## 传递的信息

- 按 presentation.xml 的真实顺序排列页面，追溯 slide → layout → master 的背景。
- 原背景图片与其他原图按内容哈希落盘；保留来源、哈希、裁切和填充方式。
- 元素类型、ID、层级、隐藏状态、位置、尺寸、旋转与翻转；普通分组坐标展开。
- 文本框内边距、自动适配设置、垂直对齐、段落层级、对齐、缩进、项目符号、行距和段距。
- run 级字体、字号、粗斜体、下划线、颜色、字符间距；解析主题字体和颜色、占位符继承。
- 形状填充、轮廓线、箭头、阴影等效果；自定义形状保留原始几何说明。
- 表格行高、列宽、合并信息、单元格内容与样式、边框和引用的表格样式定义。

位置、字号、间距统一等比换算到现有 1600×900 HTML 画布。非 16:9 模板居中适配，
记录 content_box_px，避免分别拉宽和压缩。原模板自身超出文本框的文案仍需视觉检查。

## 页面绑定与执行

页面提示词显式包含 `TEMPLATE_SOURCE_PAGE: N`，N 是模板页号，不是输出页号。
页面规划也可记录 template_source_page。扩展页数时重复适合的内容页，不能循环套封面/结束页。
缺少显式绑定时按标题语义回退并记录原因；已有页面优先保留 meta 中的绑定。

生成器直接读取磁盘 profile，将当前所选页的完整信息和其他页的简短索引加入最终模型调用。
这覆盖普通生成、逐页 brief 和 complete_page_prompts 路径，不依赖模型记住前一步的工具输出。
sample_text 仅用于槽位辨认，不作为用户事实或指令。原背景在发布前由程序写入样式层，
正文保留 HTML；明确的后续用户编辑以当前 HTML 为基础，不重新强制覆盖背景。

旧版缓存或缺失/损坏的图片会使分析器重新提取。生成阶段发现失效资料时明确要求重新分析，
不能静默换成普通主题。不改变原始上传文件，不修改已有发布工件。

## 验证边界

背景原文件复用和路径校验是确定性的；内容排版仍由模型完成，需要渲染后检查。
旋转/翻转的分组、图表/SmartArt、主题背景引用、EMF/WMF 等特殊格式不会被冒充成精确复刻；
分析结果给出 warnings。缺失字体、复杂主题效果也可能导致浏览器与 PowerPoint 有差异。
线性渐变、纯色和常规图片背景会自动恢复；其他背景类型保留信息交给生成器并要求检查。

回归覆盖：tests/algorithm/chat/workflows/test_ppt_template_html.py；实际浏览器验证位于
runtime/scripts/export_pptx/tests/template-background.test.mjs，并由现有 PPT export CI job 收集。
