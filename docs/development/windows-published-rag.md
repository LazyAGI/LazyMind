# Windows 固定复用已发布的 ModelScope RAG 组件

日期：2026-09-23。用户要求：测试并复用现有云端 Windows ZIP，不再动态打包；安装界面仅告知下载来源，不提供修改链接功能。本轮不修改 Mac 的分包方式、Skill、LazyLLM 源码或子模块 gitlink。

## 修改原因和范围

原 Windows CI 每次解析依赖、生成组件 revision，并把对应 ZIP 的 SHA 和地址写进 installer。用户云端只有旧组件时，新 installer 即使改 URL 也因 hash/manifest 不符被拒绝。因此调整构建流程，以已发布组件为固定依赖版本基线，不放宽安装时的校验。

- `desktop/python-components/windows-amd64.json`：固定云端组件清单，原 manifest/fingerprint/ZIP SHA 保持不变。
- `desktop/python-components/windows-amd64-requirements.lock`：与旧组件配套的 Windows algorithm 全部 176 项 distribution 版本，包括共用基础依赖。由已有配套环境及发布 ZIP 的 metadata 导出；构建时在 Windows venv 安装并验证，当前本地验证进度见下文。auth-service/channel-gateway 的独立 requirements 不改；不同版本仍隔离。
- `build-windows-x64.ps1`：后置模式从锁文件和现有业务 requirements 一起安装，冲突立即失败；不再运行 `lazyllm install rag` 动态选择依赖，也不运行 `build-python-components.py` 生成 ZIP。非后置的完整包对照流程保留。
- `stage-published-python-components.py`：校验 Windows x64、CPython 3.11.15、精确依赖版本/集合和 RAG 分组边界；下载固定 ZIP 并验证大小、SHA、安全解压及原 manifest。在移除 optional 文件后的精简环境实际验证基础导入和云端 overlay 导入，失败恢复被移出的文件，成功才启用原清单。
- `resume` / `resume-installer` 也核对 staging 清单与固定版本；旧 `4a318…` staging 不能直接重新封装，需执行干净构建。
- 下载缓存位于 `desktop/cache/published-python/windows-amd64/`；损坏缓存构建失败，不允许使用未验证内容。清理错误缓存后重新构建。下载与验证目录不会复制进 installer。
- 构建输出 `desktop/dist/python-components/windows-amd64/` 只有固定清单、校验文件和来源说明；Actions 继续提供 `windows-python-components` 附件以供审计，干净构建没有新 ZIP。已有上传文件无需替换。
- 前端组件弹窗显示固定 URL 和文件名，无输入框；API 只接收组件 ID，只用服务端 catalog URL 下载，自定义 URL 请求返回 400。保留取消、失败重试、安装后显式重启；修复弹窗 `confirmLoading` 导致“取消下载”被 UI 库阻止的问题。

原 `baseFingerprint` 是发布包的一部分；本轮用“固定完整依赖版本 + 构建时真实基础/overlay 导入验证”确立与它的配套关系，而不是将任意当前环境伪装为兼容。运行时仍严格检查下载 SHA、大小、平台、ABI、revision、fingerprint。未来升级任何锁定依赖必须显式验证并发布新的配套版本，不自动漂移，也不跳过校验。锁定版本并不等于永远不需要维护依赖。

## 固定云资源

- 文件：`lazymind-python-rag-windows-amd64-cp311-e262c0d2f05fe09d.zip`
- URL：`https://modelscope.cn/datasets/CarlosShaoting/lazymind-cst/resolve/master/lazymind-python-rag-windows-amd64-cp311-e262c0d2f05fe09d.zip`
- 大小：69,342,263 字节（66.13 MiB）。
- SHA-256：`258944a85d5aa29c0eb662ab21888c5c2894fdfb2c503d51f2129efa4e083bed`。

本轮实际从 ModelScope 下载并核对大小/SHA 一致，不只是验证本地旧文件。用户无需上传新 RAG ZIP。旧的 417 MiB installer 仍携带 `4a318…` 清单，必须重新构建安装；不会通过改云端文件名或覆盖旧资源使旧 installer 自动变兼容。

`LAZYMIND_PYTHON_COMPONENT_BASE_URL` 不再控制 Windows 固定组件来源；要迁移托管地址需明确修改版本清单并重建。Mac 原有构建变量与独立原生组件继续有效。

## 验证与用户验收

本轮已完成：

- 实际从 ModelScope 下载上述 ZIP，文件大小和 SHA-256 与固定清单一致。
- 固定 176 项依赖与当前 algorithm requirements 联合解析成功。
- Python 固定组件测试 5 项通过，覆盖版本变化拒绝、清单约束及失败恢复；前端组件测试 5 项通过，覆盖只读来源、取消、重试等行为。
- Go 组件相关测试通过；原生 Windows Go 测试 7 项通过，真实完整组件集成测试因未提供完整环境跳过。
- Desktop Node 测试 143 项通过、10 项跳过；PowerShell 语法及 resume 清单匹配/不匹配检查通过。

尚未完成：完整的新 Windows installer 构建、原生精简环境与云端 overlay 联合验证及应用业务验收。本机 uv 安装遇到 Windows PE launcher 资源写入错误；pip 替代安装亦未完成，因此本轮不宣称原生端到端验证通过。GitHub 构建会执行实际基础/overlay 导入检查，失败时停止打包。导入检查通过也不能替代下列业务测试。

### GitHub 构建步骤

1. 选择 `cst/installer_opt` 分支，构建引用留空，使用该分支最新提交。
2. 勾选 `Use the published RAG component from ModelScope (fixed version)`；首次使用本次修改执行完整构建，不复用旧 staging。
3. 检查构建摘要的 RAG 文件名为 `e262c0d2f05fe09d.zip` 对应完整名称，SHA 与上文一致，再下载安装包。
4. 现有 ModelScope Windows RAG ZIP 无需重新上传。`windows-python-components` 附件用于核对固定清单，不再产生需要上传的新 RAG ZIP。其他可选资源是否需上传仍按各自清单判断。


用户安装新构建后需确认：

1. “安装组件”弹窗显示 ModelScope `e262…zip`，没有链接输入框；安装时不再请求 `4a318…`。
2. 从干净的用户 runtime 安装 RAG，下载完成后显式重启；错误/断网有提示，取消后可重试；不会隐式重启正在运行的任务。
3. 普通聊天、登录、文件附件、Ark、飞书及常用 Skill 保持可用；安装 RAG 前后分别验证。
4. 新知识库 PDF/Office 入库与检索、显式落盘、退出重启后的检索和删除集合必须实际测试；保存原有数据，使用测试知识库。
5. 旧组件升级行为取决于新旧 catalog：匹配固定版本的完整安装可复用，其他 revision 不冒充兼容组件，不删除旧数据。

已知发布包含 `milvus-lite==3.0` 的 Windows `WinError 183` 持久化缺陷，原始未经裁剪 wheel 亦可复现；本轮不修改用户已上传 ZIP、第三方库内容或 checksum。该问题影响 flush/重启检索验收，需要单独修复并按受控依赖升级流程发布；不能因本轮固定版本而宣称解决。

## Mac 后续接入

本次只切换 Windows 构建的 RAG 来源；共用前后端的只读下载来源界面也会用于 Mac。Mac 目前仍沿用原有分包流程，尚未切换为固定发布包。

后续需分别为 macOS Apple Silicon（arm64）和 Intel（amd64）确认已上传 ZIP、URL、SHA、manifest 及配套版本锁；各自原生构建验证后再启用固定来源。不能复用 Windows ZIP，也不能让两个 Mac 架构共用含原生二进制的组件。接入时保留安装校验、失败恢复和构建前导入检查，并分别记录两个架构的验收结果。

2026-09-23 Mac 后续接入：Apple Silicon 已按本方案接入固定 `53a1c2e770966b71` 组件，配套 173 项依赖锁及原生验证见 [Mac 开发记录](macos-published-rag.md)。上文“Mac 尚未切换”描述的是 Windows 提交时的范围；Intel 仍待独立发布包接入。
