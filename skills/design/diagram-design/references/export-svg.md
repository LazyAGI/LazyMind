# LazyMind 独立 SVG 导出

仅当用户明确要求 SVG、矢量文件或可交给 Figma/Illustrator 的图形时读取本 Reference。第四阶段不自动导出，也不支持 PNG。

## 能力定义

- 输入必须是本 Skill 已生成并通过 `scripts/self_check.py` 的 HTML；动画 HTML 还必须通过 `scripts/verify-motion.py`。
- 输出只包含 HTML 中第一个内联 `<svg>`，不包含页面标题、摘要卡片和页脚。
- 动画 HTML 导出为完整静态最终帧；播放控件和 JavaScript 不进入 SVG，装饰动画元素被强制隐藏。
- 保留 `viewBox`、`role="img"`、`aria-labelledby`、`<title>` 和 `<desc>`。
- 添加 SVG 命名空间和批准的 Google Fonts 声明，并保留中文系统字体回退。
- 不修改源 HTML，不运行浏览器，不截图，不执行任何源内容。

## 执行流程

1. 先运行 `scripts/self_check.py` 校验 HTML；动画 HTML 再运行 `scripts/verify-motion.py`。任一失败时先修复 HTML，不要导出。
2. 使用 `write_file` 返回的绝对 HTML 路径调用：

```text
run_script(
  name="diagram-design",
  rel_path="scripts/export_svg.py",
  args=["<HTML 绝对路径>"]
)
```

默认输出到源文件旁的同名 `.svg`。需要指定新文件名时使用：

```text
args=["<HTML 绝对路径>", "--out", "<SVG 绝对路径>"]
```

`args` 必须是字符串数组。脚本返回 JSON；只有 `status` 为 `ok` 且 `output` 指向实际文件，才可继续发布。

3. 用返回的 `output` 调用：

```text
save_chat_artifact(
  filename="<文件名>.svg",
  content="<output 绝对路径>",
  content_type="file",
  caption="独立 SVG 图示"
)
```

4. 输出已存在时脚本会停止。除非用户明确要求替换，不传 `--overwrite`；优先换一个文件名。用户明确授权替换时，才可加入 `"--overwrite"`。

## 失败与边界

- 没有 `<svg>`、缺少 `viewBox`、缺少可访问标题/描述、SVG 内含脚本或结果不是合法 XML：停止并修复源 HTML。页面级固定动画控制器不会被导出。
- 一个 HTML 含多个 SVG 时只导出第一个；不要对 `assets/icons.html` 或其他图集运行。
- 某些离线设计工具不会下载 Google Fonts，会使用系统回退字体；浏览器中仍可正常显示。
- PNG 需要浏览器渲染环境。当前 LazyMind 包没有 Playwright/Chromium，不得声称能输出 PNG，也不得让用户在聊天中执行安装命令后假装已完成。
