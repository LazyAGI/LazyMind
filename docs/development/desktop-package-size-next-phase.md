# 安装包第二轮瘦身：跨电脑实施交接计划

日期：2026-09-20。原交接基线：`cst/installer_opt` 的 `e0d027638`；执行端从 `1b3e4a03` 继续实施。**五项优化及 Intel 原生构建入口已加入代码；Mac ARM64 已实际构建，Windows/Intel 原生验收仍待完成。** 下方保留原设计和验收要求，当前结果以本节实施记录为准。

开始前先在目标电脑获取 `origin/cst/installer_opt` 最新提交并核对工作区；不要覆盖同事未提交的改动。当前已完成内容、历史体积和既有测试见 [第一轮开发记录](desktop-package-size-reduction.md)，现有 Mac ARM64 操作步骤见 [安装文档](../../desktop/INSTALL.zh-CN.md)。不要把本文的计划路径或建议命令当作已经存在的功能。

## 本轮实施记录

| 项目 | 已实施 | 验证边界 |
| --- | --- | --- |
| Windows 开发二进制、LazyLLM docs 排除 | 精确删除 staging 中的 `backend/core/core`、`core.exe` 和嵌套 docs；保留源码 | 本机没有 Windows，最终 EXE 内容与业务启动待验证 |
| Windows 解释器别名 | `normalize-windows-python.py` 校验目标在捆绑解释器目录内，规范化三个 venv、复制可搬迁启动器、实际启动成功后删除 junction；Go 搬迁同步相关配置字段 | 已增加原生测试并交叉编译 Windows runtime-manager 测试二进制；原生搬迁/安装未运行 |
| Python 共享 | `share-python-dependencies.py` 比较真实文件和元数据、排除不确定包、每包独立目录和各环境相对 `.pth`、导入/metadata 复查、重复执行及损坏检查 | 默认关闭，开关 `LAZYMIND_DESKTOP_SHARE_PYTHON=true` / Actions `share_python=true`；本次 ARM64 开启并通过 |
| PDF 字体 | 固定 catalog、构建生成 TTF 和许可证；桌面 staging 移出字体；Core 已鉴权接口下载、SHA/大小验证、超时、并发去重、原子缓存及坏缓存重试；前端错误可重试 | 真实 TTF 经本地 HTTPS/处理器验证；生成 PDF 可提取中文。云端未上传、完整界面回归待完成 |
| Mac Intel | `make desktop-darwin-x64`、`desktop-darwin-x64-dmg`，原生架构检查、amd64 manifest/组件目录、x64 Electron、开发路径和清理入口 | 官方飞书 CLI 下载 SHA 和 x86_64 Mach-O 已核对；完整 Intel 应用没有在本机运行 |

Mac 完整构建：Node 20.20.2、pnpm 10.34.5、Python 3.11.15，ad-hoc ZIP，原三个优化开关开启，额外开启共享。最后拆出的 RAG 与第一轮文件和 SHA 完全一致：`lazymind-python-rag-darwin-arm64-cp311-53a1c2e770966b71.zip` / `f90b5c00b43943b031d698fc939c81b24d77a738e357bb541fe74d7e768fb8d1`，本次无需重传该组件。

- 本机第一轮 ZIP：670,748,589 字节 / 639.68 MiB。
- 第二轮 ZIP：652,231,633 字节 / 622.02 MiB；比上述本机产物减少 17.66 MiB（2.76%）。这是本机前后构建结果，包含代码/资源变更，不作为严格锁定全部输入的 A/B 结论，也不能推算 Windows EXE 节省。
- 最终 runtime：1254.61 MiB 展开；相同纯 Python 依赖共享减少 8,648,034 字节 / 8.25 MiB 展开；字体移出 17,772,300 字节 / 16.95 MiB。两者不能直接从压缩包中相减。
- 报告：`desktop/build/darwin-arm64/final-runtime-size.json`；共享报告在最终 `.app/Contents/Resources/runtime/config/python-sharing.json`。旧已导入环境的共享试验仅节省 5.26 MiB，因为有额外缓存，保守规则跳过更多包；正式记录使用干净构建结果。
- 最终 `.app` 使用已上传 RAG 的真实云端下载完成六阶段验证，随后 `codesign --verify --deep --strict` 通过；报告为 `desktop/dist/component-check/darwin-arm64/second-round-cloud-report.json`。
- 独立共享环境搬迁到中文/空格新路径后，基础导入、RAG、Milvus 写入/flush/重启检索/删除全部通过。该目录已改变，原试验路径不再可用。

测试：Desktop 150 通过 / 3 Windows 专属跳过；Python 裁剪/组件/共享 24 通过 / 2 Windows 专属跳过；Windows alias 原生新增测试在 Mac 跳过；Core systemdeps、runtime-manager 全量通过；字体接口 race 测试通过；前端字体加载/重试/Web 回退 2 项通过，前端生产构建通过。没有调用真实模型 API，也没有宣称安装、登录、飞书、Skill、PDF 翻译等全量业务验收完成。Windows 既有 Milvus `WinError 183` 未修复，不能标记 Windows RAG 持久化通过。

新上传资源只有跨平台字体：`desktop/dist/pdf-font/lazymind-pdf-NotoSansSC-a3041811a78c361b.ttf`，SHA-256 `a3041811a78c361b1de50f953c805e0244951c21c5bd412f7232ef0d899af0da`。目标是既有 ModelScope 数据集 master 根目录；目前尚未上传。操作及确切 URL 见 [安装文档第 11 节](../../desktop/INSTALL.zh-CN.md#11-第二轮资源共享开关与-windows-操作)。不提交构建产物或 LazyLLM gitlink。

## Windows 接手端原生补测（2026-09-20）

从另一台电脑拉取 `446a49c02` 后，在 WSL 宿主的真实 Windows CPython 3.11.15 x64 上补测，未重新实现已有去重/共享功能。

- 解释器 alias/junction 规范化原生测试 1 项通过，覆盖中文/空格路径、三个 venv 启动、保留真实解释器及重复执行。
- 共享依赖原生测试 7 项通过，覆盖共享后实际导入、metadata/资源读取、移动目录后启动、版本/内容隔离、损坏检测与重复执行；同组 Linux 测试 7 项也通过。
- runtime-manager Windows 测试二进制原生执行 3 项通过：venv 路径搬迁及附加配置字段、不变只读文件、被锁文件替换报错。
- 初次共享测试有 2 项因 UTF-8 中文资源被隔离子进程按 GBK 读取而失败。已显式指定测试资源和子进程编码；同时将生产规范化脚本的 `pyvenv.cfg` 读写固定为 UTF-8，避免中文构建路径按系统默认编码写坏。以上最终 Python 原生测试使用 `-X utf8=0` 运行，确保配置处理不依赖构建机开启 UTF-8 模式。
- **边界：** 这些是原生脚本/模块测试，不代表本次已构建完整 Windows EXE，也不代表三个真实业务环境及安装后全部流程已验收。Windows installer 首次 warmup、真实服务、升级与 RAG 持久化仍需原验收清单。依赖共享开关仍默认关闭；Actions 勾选 `share_python` 才会应用共享，解释器别名去重则默认执行。

## 约束与交付平台

- 不修改 Skill 代码、资源、安装流程，不后置 Skill Review 依赖的 UMAP/Numba/llvmlite 及其科学计算依赖。
- **Never commit lazyllm subm in Lazymind repo。** 不暂存或提交 `algorithm/lazyllm` 的 gitlink；现有工作区子模块指针差异不是本轮改动。以下五项不需要改 LazyLLM 源码；若额外任务确需修改，先在子模块创建 `cst/installer_opt` 分支，独立交付。
- 云资源托管是 **ModelScope**；DashScope SDK 保持原样。用户自己上传，执行 GPT 负责生成资源、精确文件名、SHA-256 和目标 URL，不替用户发布。
- 不把构建目录、安装包、字体或依赖 ZIP 提交进 Git。保留既有 RAG 和案例机制、用户数据及 Mac 签名修复。

| 发布目标 | 名称映射 | 当前构建入口 | 本轮要求 |
| --- | --- | --- | --- |
| Windows x64，唯一 Windows 版本 | Electron `x64`，Go/catalog `amd64` | `desktop/scripts/build-windows-x64.ps1` | 继续生成 x64 installer；不增加 Windows ARM64 |
| Mac Apple Silicon，M 系列 | `arm64` | `make desktop-darwin-arm64` / `desktop-darwin-arm64-dmg` | 保留原生 ARM64 构建、组件签名、包外拆包及最终签名顺序 |
| Mac Intel | 系统/Python `x86_64`，Electron `x64`，Go/catalog `amd64` | `make desktop-darwin-x64` / `desktop-darwin-x64-dmg` | 原生 Intel 构建待验收，已补入口及校验；不能把 ARM64 产物改名，不能只改 Electron 参数 |

两个 Mac 架构是两个独立构建，不是已经提供 Universal 包。先在相应原生机器构建验证；CPU 架构与最低 macOS 版本分别核实，不能根据 Electron 支持范围推断 Python wheels 的兼容范围。本次不发布 Linux。

## 基线和收益如何计算

主仓报告 **546.08 MiB**，第一轮优化版 **448.30 MiB**，减少 **97.78 MiB / 17.9%**。下面大小来自源码或 Python 展开目录审计，**不能相加后直接从 EXE 大小扣除**，第二轮实际收益要以新 installer/ZIP/DMG 为准。

Python 原报告在 RAG 拆出、解释器别名处理和最终封装之前生成，因此榜单中仍出现 spaCy/PyArrow/FAISS 不代表最终 installer 仍带着它们，不能重复计算节省。第二轮增加最终 staging 报告，分别统计应用源码、解释器、三个 venv、共享目录、外置 RAG 和字体；同时记录最终压缩产物大小。

## 1. Windows 排除仓库里的 Mac 开发二进制

**做什么：** Windows 应用 staging 排除 `backend/core/core`，并继续排除 `backend/core/core.exe`。只处理构建副本或复制规则，保留仓库源文件。

**为什么：** `backend/core/core` 是已跟踪的 Mac ARM64 Mach-O 文件，23,628,210 字节（22.53 MiB）。Windows 的 `Copy-RuntimeApp` 会复制后端目录，目前只删除 `core.exe`，会顺带携带这个不能在 Windows 执行的开发产物。正式使用的 Core 已由 Go 单独构建在 `runtime/bin/core.exe`。

**具体怎么做：**

1. 修改 `desktop/scripts/build-windows-x64.ps1` 的 `Copy-RuntimeApp`：按精确路径排除，或在复制后删除 staging 中的这两个文件；不要全局删除名为 `core` 的目录/文件。
2. 保留 `Build-GoBinary` 输出的 `runtime/bin/core.exe`，不改变启动配置。Mac 已有相应开发产物清理，无需删除源码补丁。
3. 对最终 Windows staging 或解开的 installer 检查：`app/backend/core/core` 与 `core.exe` 均不存在，`bin/core.exe` 存在且架构正确。启动 Core，验证健康检查、登录和聊天。

## 2. Windows 排除 LazyLLM 文档

**做什么：** Windows 不复制 `algorithm/lazyllm/docs/`，与现有 Mac 清理规则保持一致。

**为什么：** 主仓记录的 LazyLLM 版本，其跟踪文档约 18.68 MiB。当前 Windows 的根 `docs` 排除规则是精确路径，不能排除嵌套的 LazyLLM 文档目录。运行时使用 `algorithm/lazyllm/lazyllm/`，无需文档图片与教程附件。

**具体怎么做：**

1. 将仓库绝对路径 `algorithm/lazyllm/docs` 加入 `Copy-RuntimeApp` 的 `$excludedDirs`；必要时对旧 staging 做定向清理。
2. branch 构建继续保留 LazyLLM 源码、license 和运行时数据；tag/release 现有不带源码的逻辑保持原样。不修改子模块内文件。
3. 检查 branch、release 两种构建路径；验证无文档目录，基础 LazyLLM 导入、聊天、Ark 图片/视频及安装 RAG 后的导入不变。第一项与本项合计约 41.21 MiB 展开内容，压缩后收益待测。

## 3. Windows Python 解释器别名去重

**做什么：** 相同解释器只保留一份真实目录，三个 venv 统一引用；不要把 uv 的目录别名再复制成完整解释器。

**为什么：** `Materialize-PythonAliases` 目前把 `runtimes/python` 中的 reparse point 复制为普通目录。一个版本目录和指向它的别名可能因此变成两份相同解释器。报告中单份约 46.82 MiB 展开大小，实际重复数量及收益需以本次 staging 核实。

**具体怎么做：**

1. 审计 `Materialize-PythonAliases`、各 venv 的 `pyvenv.cfg` 和 `Scripts` 启动器，记录别名、真实目标、Python 版本。只处理指向同一捆绑解释器的别名；目标必须在当前 staging 内，拒绝外部目标、循环或无法解析的链接。
2. 替换复制别名的逻辑：先规范化三个 venv 的 `home`，必要时同步 `executable`/`base-executable` 等相关路径；路径计算使用当前构建根目录，不写死 GitHub runner 或本机目录。
3. 检查 uv 启动器是否仍依赖旧路径；若需要换为可搬迁解释器，复用已有 Windows venv relocation 规则。所有 venv 经真实 Python 启动验证后再移除别名；构建失败直接退出，不能删除失败后继续封装。
4. 核对 `local/local-runtime-manager/python_venv_windows.go`：它目前用 `filepath.Base(oldHome)` 拼接安装后 home，必须能找到规范化后的目录；保留旧版本包的搬迁兼容。新打包结果不得残留 junction/reparse point。
5. 在构建机把完整精简 runtime 复制到一个含空格/中文的新路径，使原构建路径不可访问，再走实际 warmup/relocation，分别启动 algorithm、auth-service、channel-gateway。不能只在原构建目录测试。
6. 验证 `New-DeferredPythonRuntimeStage` 的 `python-runtime.zip` 只有一份对应真实解释器，解压后三个服务可用；覆盖首次安装、重启、覆盖升级及 `resume-installer`，重复执行规范化应安全。

**Mac 注意：** Mac 现有相对符号链接应保留，不套用 Windows 的删除策略。Mac 只检查链接在整个 runtime 移到 `.app` 外拆包、再移回后仍指向捆绑解释器。

## 4. 三个 Python 环境的相同依赖共享

**做什么：** 保留三个 venv 的逻辑隔离，审计并共享可证明相同的依赖。先实现纯 Python wheel 的保守共享；含原生库、启动钩子或不确定导入行为的包继续原样保留，并记录跳过原因。

**为什么：** 三个环境会重复安装依赖，但同名不代表同版本或相同内容。例如现有审计中 algorithm 的 cryptography 为 45.0.5，其他环境为 44.0.2，不能合并。盲目合并整个 site-packages 会改变依赖解析，影响认证、消息网关或 Skill。

**具体怎么做：**

1. 新增独立构建审计/共享脚本，读取各环境的 distribution metadata、版本、WHEEL、RECORD，并计算实际文件 SHA-256。输出候选、共享字节数、环境列表和跳过原因；不能只比较包名与版本。
2. 第一版只共享内容完全一致、独占顶层包目录的纯 Python distributions。排除 pip/setuptools 等安装工具、`.pth` 启动钩子、namespace packages、跨 wheel 共用目录、符号链接/外部路径、原生 `.pyd/.dll/.so/.dylib` 及本次不应改动的 LazyLLM。RECORD 之外的实际文件也要核实，避免误删其他包或运行时数据。
3. 建议共享位置：`runtime/deps/python/shared/<内容哈希>/`，每个目录只放该匹配 distribution 的代码、资源、metadata 和 license；各 venv 的 site-packages 用**相对路径 `.pth`**引用它真正拥有的那一份。
4. 不要把所有共享包放在一个对三个环境全局可见的目录；这会让某个环境看见原本未安装或版本不同的依赖。console entry point 和 RECORD 外部路径要明确保留或跳过；不在用户端运行 pip 修补。
5. 顺序放在既有裁剪和 RAG 分包之后、最终封装之前，避免分包脚本按原 site-packages 扫描时漏算 distribution。检查 catalog/fingerprint 是否仍准确反映逻辑基础依赖；如调整 fingerprint 算法，要同步构建和运行时兼容判断，生成配套新组件。
6. Windows 的 shared 放进现有 `deps/python` payload 范围；Mac 在最终 `.app` 拆包阶段、runtime 已移到包外时处理，且在最终签名前完成。不能在已签名 `.app` 中运行修改脚本。
7. 先复制并校验共享副本，再写引用和移除重复文件，出错中止打包；确保重新运行及 resume 不重复搬运、不丢报告。建议提供单独开关关闭共享以做同提交对照，默认值在验证后确定。
8. 为三个 venv 分别验证实际 import 路径、`importlib.metadata.version`、资源读取和 entry points；同版本同内容共享，不同版本、同版本不同内容保持隔离。把 runtime 移到新路径复测，再验证安装 RAG 前后和业务启动。

**验收重点：** `.pth` 不能依赖绝对构建路径；共享目录不能越界；算法用到的 UMAP/Numba/llvmlite 仍可用；登录/认证和飞书网关都可启动。共享收益以真实报告为准，不预设可减少几十 MiB。若纯 Python 共享收益很小，记录实际结果，不为数字强行合并 native 库。

## 5. PDF 中文字体后置下载与缓存

**做什么：** 桌面安装包移出 `NotoSansSC-wght.ttf`，首次需要生成中文 PDF 时下载校验并缓存，后续离线复用；下载失败允许重试。

**为什么：** `frontend/public/fonts/NotoSansSC-wght.ttf` 约 16.95 MiB。`frontend/src/modules/knowledge/utils/pdfDocumentRenderer.ts` 将它作为 TTF 交给 jsPDF 嵌入 PDF，不能直接删掉，也不能简单替换为 WOFF2。后置节省首次安装下载量，但首次 PDF 导出仍需下载字体。

**具体怎么做：**

1. 保留源码字体及 `NotoSansSC-OFL.txt`，新增固定下载描述（文件名、大小、SHA-256、HTTPS URL）。构建时从这份字体生成带内容哈希名称的上传文件和校验清单；如使用原始 `.ttf` 下载，不再额外套 ZIP。
2. 建议输出 `desktop/dist/pdf-font/`，Win、Mac ARM64、Mac Intel 共用同一字体资源，只需用户上传一次。默认地址沿用 `https://modelscope.cn/datasets/CarlosShaoting/lazymind-cst/resolve/master/` 加实际生成文件名；不要在文档虚构已上传链接或手填占位哈希。
3. 只在桌面 runtime 的 `app/frontend/dist/fonts/` 移除 TTF，保留许可证；不要删除源码 `public/fonts` 或改变 Web/Docker 的静态字体行为。检查最终 bundle 其他位置是否还有同一字体副本。
4. 建议新增本地 Core 字体接口，复用现有本地 runtime 目录与 HTTPS 下载/校验方式，缓存到用户 runtime 的 `deps/pdf-font/<sha>/`，不写签名 `.app`。沿用现有鉴权/授权机制，不开放任意 URL 或文件读取。
5. 下载要有超时、大小限制、SHA 校验、HTTPS 重定向检查、临时文件与完成后原子启用；并发首次请求只下载一次；失败不启用半成品。缓存损坏应识别并重新下载，Windows 文件替换需避免已有目标导致 `os.rename` 失败。
6. 前端桌面模式通过带鉴权的现有 API client 获取字体二进制，避免 ModelScope CORS 依赖；非桌面模式仍用原静态路径。首次加载显示字体准备状态/联网提示，错误可读，不把字体加载失败的 Promise 永久缓存。
7. 上传资源由用户完成，构建时不用访问尚未发布的字体 URL。交付应列出准确上传路径、文件名、URL、大小、SHA 和许可证；上传前本地 HTTPS 测试，用户上传后验证真实云端下载。

**验收重点：** 首次在线导出中文可搜索 PDF/翻译 PDF；退出并重开后断网导出；首次断网、404、坏哈希、取消/超时后重试；并发导出；无中文缺字；普通 PDF 阅读不提前下载。Mac 下载后 `.app` 签名仍有效，Web/Docker 导出不回归。

## Mac Intel 构建适配清单

以下为原设计检查清单，现已按上述实施记录补齐代码入口，共用现有 Mac 构建脚本并添加 x64 薄入口；原生 Intel 构建和业务回归仍待验收。

| 位置 | 应做修改与检查 |
| --- | --- |
| `desktop/scripts/build-darwin-arm64.sh` / 新共用脚本、`Makefile` | 新增 Intel 入口，建议 `make desktop-darwin-x64`、`desktop-darwin-x64-dmg`；校验 `uname`、Node、Go、Python 架构一致，拒绝错误宿主/Rosetta 混用；build/dist 分开 |
| `backend/core/providerconnection/feishu-cli-release.json` | 补 `darwin-amd64` 官方资产哈希，下载对应 CLI；不复用 ARM64 URL/哈希 |
| Python、Go/CGO 服务 | 使用原生 x86_64 Python 3.11.15 与其 wheels，Go `amd64` 二进制；核实原生依赖实际支持的最低 macOS 版本 |
| `desktop/scripts/write-runtime-manifest.mjs` | 支持并测试 `darwin/amd64`；检索运行时其他 target 白名单并同步 |
| `desktop/electron/package.json` | 加 Electron `--x64` 的目录/DMG 脚本；核实实际输出目录，不能假定 x64 与 arm64 的 electron-builder 目录命名一致 |
| `desktop/electron/electron-builder.config.cjs` | RAG 输出不再硬编码 `darwin-arm64`，根据真实打包目标生成 `darwin-amd64`；保留包外拆包、原生库签名与主应用签名的原有顺序 |
| `desktop/electron/src/main.js` | 本地开发 runtime 默认路径目前固定 `darwin-arm64`，随运行架构选择；检查相关 dev-runner/clean 脚本是否同样硬编码 |
| CI 与文档 | 如增加 Intel CI，明确选择可用原生 runner；artifact 名区分架构。未跑 Intel 构建时只能写“入口已实现，真机待验证” |

2026-09-20 已只读核对 [飞书 CLI v1.0.93 官方发布](https://github.com/larksuite/cli/releases/tag/v1.0.93) 及其 [checksums.txt](https://github.com/larksuite/cli/releases/download/v1.0.93/checksums.txt)：存在 `lark-cli-1.0.93-darwin-amd64.tar.gz`，官方列出的 SHA-256 为 `bf37861ce5b5fb10c093ffd8b7305f2a80349280cf32563267c29f81cb864e53`。执行端现已下载并核对上述 SHA 与 x86_64 Mach-O 架构，已更新版本清单；未在 Intel 机器运行完整应用。

最终三个 RAG 目录分别为 Windows `windows-amd64`、Mac ARM64 `darwin-arm64`、Mac Intel `darwin-amd64`，每份均绑定同次构建 installer 的 catalog，不能互换。字体与 Workflow 五案例属于跨平台资源，和这些原生组件区分。

## 原实施顺序与后续验收清单

1. 拉取最新分支并确认协作改动，先做第 1、2 项；构建 staging 检查排除范围，不更改 Skill 或子模块。
2. 做第 3 项，在 Windows 原生环境验证搬迁与三个 venv 启动，再处理第 4 项共享；两项分开记录报告，便于定位问题。
3. 实现字体下载/缓存及上传资源生成，完成失败与离线复用测试。
4. 补齐 Mac 两个架构入口，ARM64 和 Intel 分别做原生构建、RAG 配套检查与签名验证；无法在本机实测的平台明确列为待验收。
5. 更新 `desktop/INSTALL.zh-CN.md`，按 Windows x64 / Mac ARM64 / Mac Intel 分栏写真实可执行步骤、输出位置、上传文件和验证命令；实现完成前保留“Intel 入口尚未实现”的事实。
6. 每个平台用 `verify-python-components.py` 验证最终精简 runtime 和同次 RAG ZIP，再用云端资源验证。不要用尚未拆包的中间 Python 环境冒充最终安装环境。
7. 增加上述功能相关的回归测试，不用静态字符串断言代替实际导入/搬迁/下载验证；检查已有 desktop 脚本、runtime-manager、Core systemdeps 和前端字体调用测试。
8. 真实应用回归：干净安装与升级、首次 warmup 和五案例、三个服务启动、登录、聊天、Ark、飞书连接、已有 Skill、RAG 安装前后、PDF/Office 入库与检索、重启后检索、PDF 导出。
9. 记录同提交/同依赖的开关对照，提供最终 installer 大小、最终 staging 各类大小、共享报告、字体大小；旧报告中的 RAG 和测试清理收益不能重复算。
10. 交付代码修改清单、测试通过/失败/未运行项、精确上传文件、用户测试步骤。提交前检查暂存列表，确保不含 `algorithm/lazyllm`；不要用未过滤的 `git add -A`。

**已知问题必须交接：** 现有 `milvus-lite==3.0.0` 在 Windows 用 `os.rename` 覆盖已有 manifest 导致 `WinError 183`，未经裁剪的原始 wheel 也可复现。它与本轮体积优化不是同一问题，但 Windows RAG 持久化验收尚未通过；不能只测导入和即时检索就写“全部正常”。由执行端另行处理并验证显式 flush、重启后加载/检索及删除集合，记录是否变更依赖版本/组件哈希。不要静默修改第三方文件或沿用旧组件 checksum。
