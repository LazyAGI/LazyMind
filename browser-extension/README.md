# LazyMind Browser Extension

当前目录是无需构建即可加载的 Chrome/Edge Manifest V3 开发版扩展。Chrome 与 Edge 共用同一个 Manifest、Gateway 协议和控制器；扩展会在配对时自动识别当前浏览器，并把 `Google Chrome` 或 `Microsoft Edge` 及浏览器版本上报给 LazyMind。

## 本地加载

1. 启动 LazyMind Docker，默认入口为 `http://127.0.0.1:8090`。
2. Chrome 在地址栏打开 `chrome://extensions`；Edge 打开 `edge://extensions`，然后启用“开发人员模式”。
3. 选择“加载已解压的扩展程序”，目录指向本目录 `browser-extension/`。
4. 在已登录的 LazyMind 中调用 `POST /api/core/browser/manage/pairings` 生成配对码。
5. 打开扩展，填写 LazyMind 地址与配对码；浏览器会在配对时一次性询问网页读取权限，之后无需逐站点授权。
6. 如需控制页面，直接在 LazyMind 对话中提供 URL；扩展会自动打开并只控制它为任务创建的窗口。网页读取权限可随时在扩展弹窗中关闭。

## Desktop 依赖安装

Desktop 用户可以在“设置 → 系统工具 → 依赖安装 → 浏览器控制扩展”中安装扩展包。安装目录为 Desktop Runtime 下的 `deps/browser-extension`，安装完成后可点击“打开安装位置”。

Chrome/Edge 安全策略不允许 Desktop 在普通个人浏览器中静默启用扩展；仍需打开对应浏览器的扩展管理页、启用开发者模式并选择“加载解压缩的扩展”。正式商店版本发布后可分别改为 Chrome Web Store 或 Microsoft Edge Add-ons 安装；企业受管 Edge 可再使用 `ExtensionInstallForcelist` 部署。

## Edge 本地加载

1. 在 Edge 地址栏打开 `edge://extensions`。
2. 打开左侧“开发人员模式”。
3. 点击“加载解压缩的扩展”，选择本目录或 Desktop 安装出的 `deps/browser-extension` 目录。
4. 打开扩展弹窗，确认标题下方显示 `Microsoft Edge <版本>`。
5. 在 LazyMind“设置 → 系统工具 → 依赖安装 → 浏览器控制扩展”中点击“配置”，选择 Microsoft Edge，生成配对码并连接。
6. 配对时一次性同意网页读取权限；之后可直接抓取当前 Edge 标签页，不需要逐站点授权。控制新页面时，扩展会创建最大化的独立 Edge 窗口。

Edge 开发版不需要单独复制一套源码。用于 Edge Add-ons 提交的 ZIP 也从本目录生成，避免 Chrome/Edge 两套控制器产生行为差异。

## 快照诊断

- 扩展 Service Worker 控制台会输出 `[LazyMind Browser] snapshot`，包含原始 AX 节点数、返回元素数、零宽文本节点数、角色分布、快照耗时和响应字节数。
- Core 日志会输出 `[Browser] [COMMAND]`，包含动作名称、端到端耗时、请求/响应字节数、快照 revision、元素数和角色分布；不记录页面正文。
- 纯零宽字符等无可见内容的可访问性节点会被过滤，避免复杂表格用无效 `StaticText` 填满 500 个元素上限。

## 当前自动化边界

- 当前标签页仅支持读取，不能被 Agent 静默控制。
- 控制只针对扩展创建的 managed window/tab。
- Agent 打开 URL 时创建独立窗口，并默认以最大化状态显示。
- 测试阶段启用完全自动化：允许密码、OTP、支付与身份字段输入，也允许提交、发送、发布、删除、购买、付款、授权和 Enter 提交。
- 原高风险、敏感字段和按键白名单判断保留为代码注释，后续需要审批模式时可恢复。
- 仅允许 `http/https` URL；本地和内网 URL 需要工具调用明确设置 `allow_private_network`。
- 页面文本和 Accessibility 快照一律作为不可信内容返回。
- 当前请求配置了 `vlm` 时，Chat 侧会注入只读的 `browser_visual_inspect`。它会把当前截图写入临时文件并调用 VLM，用于理解画面中的图表、Canvas、图片、弹窗或错误状态；主模型只接收结构化观察结果，不接收截图 Base64。没有 VLM 时不会暴露该工具，基础浏览器读取和 DOM/Accessibility 控制不受影响。
- VLM 不提供点击坐标，也不参与实际操作。点击和输入始终使用 DOM/Accessibility ref、`browser_click_intersection`、`browser_type_focused` 等确定性路径，并用 `verify_text` 检查文字确实出现在页面中。
- 对“人员行 × 日期列”这类空单元格没有 Accessibility ref 的网格页面，可用 `browser_click_intersection` 传入唯一的行标签 ref 和列标题 ref。扩展读取两者现场几何位置并点击交点，不依赖 VLM；结果会返回行列名称、矩形、交点及点击前后的命中/焦点摘要。
- VLM 只读理解可补充说明 Canvas、图表、图片、弹窗和错误状态；跨域 iframe、Canvas 交互、遮挡/动画状态及视觉歧义仍可能需要重试或人工接管。

## 尚未包含

- Chrome Web Store/Edge Add-ons 正式上架与签名包。
- Desktop Native Messaging；Desktop MVP 暂时通过本地 HTTP/WebSocket 代理。
- 设备凭证数据库持久化；Core 重启后当前仍需重新配对。
- 长正文对象存储；当前通过 `next_offset` 分页读取全文，每页最多 40 万字符，并用全文 SHA-256 校验分页期间页面是否变化。
- 可切换的高风险操作审批 UI；当前产品决策为完全自动化。
- 录制操作转 Skill。
