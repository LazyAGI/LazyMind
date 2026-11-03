# Mac 本地构建、上传依赖与安装验证

## Mac RAG 签名验证

Developer ID 构建始终使用本次签名后的原生依赖重新生成 RAG ZIP，不能复用历史固定 ZIP。
构建时检查 ZIP 内每个 Mach-O 的有效签名和 TeamIdentifier，要求与运行验证的 Python 一致；
保留 Hardened Runtime、Library Validation 和 ZIP SHA-256 校验。
正式 RAG 发布必须配置 `MODELSCOPE_API_TOKEN`；HF 镜像按需配置 `HF_TOKEN`。DMG 安装测试使用安装后的 Python，从清单 URL 下载组件，
再次检查 SHA、Team ID、RAG 导入及 Milvus 写入、重启和检索；报告保存为
`macos-installed-rag-verification` artifact。该测试使用临时数据，不修改用户知识库，
也不替代界面“安装组件”及服务重启的交互测试。
个人仓库的 ad-hoc 开发构建仍可使用原有流程，但不证明官方 Developer ID 签名链路通过。

## Actions 自动发布 RAG

macOS / Windows Actions 都生成与当前安装包配套的 RAG ZIP。在 GitHub 仓库
`Settings → Secrets and variables → Actions` 中配置对应数据集的写权限：

- ModelScope 主源使用 secret `MODELSCOPE_API_TOKEN`，repository variable `LAZYMIND_MS_COMPONENT_REPO` 默认 `CarlosShaoting/lazymind-cst`。
- Hugging Face 镜像使用 secret `HF_TOKEN`，repository variable `LAZYMIND_HF_COMPONENT_REPO` 默认 `LazyAGI/LazyMind`。
- Mac Developer ID 正式构建和 Windows release tag 构建缺少 MS 主源 token 时发布失败；开发构建缺少对应 token 时跳过该源。HF 镜像缺少 token 时跳过。

两个源由独立 job 发布到各自的 `RAG/tags/<tag>/`：先上传原始 ZIP 和 `SHA256SUMS`，再上传 `python-components.json`，最后从公开 URL 下载回验。Mac 的宿主 Python 和组件内 Mach-O 使用同一个 Developer ID 签名，签名完成后才计算 ZIP 的 SHA 和 revision。

客户端在安装组件时读取 tag 对应的在线清单，并校验平台、架构、Python ABI、依赖 fingerprint、大小、SHA 和真实导入。安装成功后按保存的 descriptor 离线复用。旧客户端需要升级才能使用在线清单，不能通过覆盖旧 ZIP 绕过兼容性检查。

构建完成后，安装测试、RAG 发布和安装包镜像并行执行。安装测试使用同次构建的组件 Artifact；用户首次在线安装 RAG 仍需等待对应发布 job 成功。旧包和旧 tag 清单保留给旧客户端使用。

## Windows 本地构建与复测

在原生 Windows 的本地磁盘目录检出同一分支并初始化主仓记录的 LazyLLM 子模块，使用 PowerShell 7、Node.js 20、pnpm 10.0.0、Go 1.26.5 和 uv 0.11.31。不要复用 Linux 的 `node_modules`、Python venv 或通过 WSL UNC 路径执行 Windows 打包。

```powershell
corepack enable
corepack prepare pnpm@10.0.0 --activate
git submodule update --init algorithm/lazyllm
pwsh -NoProfile -File desktop/scripts/build-windows-x64.ps1 doctor
pwsh -NoProfile -File desktop/scripts/build-windows-x64.ps1 installer
```

只验证已构建的精简 runtime 和本地组件时，使用该 runtime 自带的 Python：

```powershell
$runtime = (Resolve-Path 'desktop/build/windows-x64/runtime').Path
$python = Join-Path $runtime 'deps/python/algorithm/Scripts/python.exe'
& $python -B desktop/scripts/verify-python-components.py --runtime $runtime --bundle-dir desktop/dist/python-components/windows-amd64
```

去掉 `--bundle-dir` 可验证清单中的实际下载地址。脚本使用临时数据检查依赖导入、ZIP 校验及 Milvus 持久化，成功返回退出码 0；应用安装和界面操作仍需单独验证。

产物位于 `desktop/dist/LazyMind-windows-x64-installer-*.exe`，配套 RAG 位于 `desktop/dist/python-components/windows-amd64/`。如果仅在末尾封装失败，确认依赖、分包和 staging 已完整完成后，可使用 `resume-installer`；它仍执行 RAG SHA、导入和 Milvus 持久化校验。依赖或应用代码变动时应重新执行完整 `installer` 构建。

## 0. 确认目标架构

在目标 Mac 的原生终端运行：

```bash
uname -m
node -p process.arch
sw_vers -productVersion
```

| 目标电脑 | 架构 | 当前流程 |
| --- | --- | --- |
| M 系列 Mac | `arm64` | 本文第 1～8 节可用；不同代 M 芯片无需分别打包 |
| Intel Mac | `x86_64`，Node 显示 `x64` | 使用第 9 节原生 x64 入口，生成独立的 `darwin-amd64` RAG 组件 |

**按目标架构选择入口，并在对应原生 Mac 上构建。** ARM64 使用 `make desktop-darwin-arm64`，Intel 使用 `make desktop-darwin-x64`。脚本检查宿主、Node、Go 和 Python 架构，拒绝 Rosetta 或交叉混用；内部共用流程按目标选择飞书 CLI、manifest、Electron 和输出路径。不能把 ARM64 ZIP 改名为 x64。

CPU 架构和 macOS 版本分别核对；目标系统需满足锁定依赖及原生 wheel 的最低系统版本要求。

## 1. 准备构建环境和代码

需要 Xcode Command Line Tools、Git、make、Node.js 20、pnpm 10、Go 1.25 或更新的兼容工具链，以及 uv。与仓库 CI 保持一致可减少环境差异；Python 3.11.15 由构建脚本通过 uv 准备，不需要手动安装 Python 依赖。桌面构建不需要 Docker。

```bash
uname -m                  # 必须是 arm64；不要在 Rosetta 的 x86_64 终端中构建
xcode-select -p            # 未安装时先执行 xcode-select --install
git --version
make --version
node --version            # 使用 Node.js 20
node -p process.arch       # 应为 arm64
pnpm --version            # 使用 pnpm 10
go version
uv --version
```

新电脑首次获取代码：

```bash
git clone https://github.com/LazyAGI/LazyMind.git
cd LazyMind
git submodule update --init algorithm/lazyllm
git rev-parse HEAD
git submodule status algorithm/lazyllm
```

已有独立构建仓库时，先确认工作区改动已妥善保存，再更新当前分支：

```bash
git pull --ff-only
git submodule update --init algorithm/lazyllm
git status --short
```

构建前确认主仓库版本与子模块检出一致。使用独立构建目录，不复用其他电脑的虚拟环境、`node_modules` 或构建产物。

## 2. 选择一种打包方式

在仓库根目录设置本地构建参数：

```bash
export LAZYMIND_DESKTOP_PRUNE_PYTHON=true
export LAZYMIND_DESKTOP_DEFER_PYTHON=true
export LAZYMIND_DESKTOP_DEFER_HISTORY=true
export LAZYMIND_RELEASE_BUILD=false
# 可选：纯 Python 相同依赖共享；默认 false，仅审计不移动
export LAZYMIND_DESKTOP_SHARE_PYTHON=true
```

`LAZYMIND_RELEASE_BUILD=false` 使用本地源码构建方式，随应用带上主仓库记录的 LazyLLM 源码。本地测试不需要创建 release tag。

**自己先测试：选择 ZIP。** 不需要 Developer ID 证书，使用 ad-hoc 签名：

```bash
LAZYMIND_DESKTOP_PACKAGE_KIND=zip \
LAZYMIND_DESKTOP_SIGNING_MODE=adhoc \
make desktop-darwin-arm64
```

需要保存完整构建日志时，可用下面命令替代上面的构建命令；`pipefail` 确保构建失败不会被 `tee` 掩盖：

```bash
mkdir -p desktop/dist/build-logs
set -o pipefail
LAZYMIND_DESKTOP_PACKAGE_KIND=zip \
LAZYMIND_DESKTOP_SIGNING_MODE=adhoc \
make desktop-darwin-arm64 2>&1 | tee desktop/dist/build-logs/build-mac-arm64.log
```

脚本依次下载并校验飞书 CLI、编译 Go 服务、构建前端、安装 Python 3.11.15 及依赖、裁剪 Python、准备内置资源和案例下载描述、组装 Electron、拆出 RAG、签名并压缩应用。首次构建需要联网且下载较多；看到依赖安装完成不代表最终打包完成。结束时必须返回退出码 0，且打印 `.app` 和 ZIP/DMG 路径。

**已有 Developer ID Application 证书：可以选择签名 DMG。** 先确认登录钥匙串中的身份，再构建：

```bash
security find-identity -v -p codesigning
make desktop-darwin-arm64-dmg
```

本地 DMG 命令执行 Developer ID 签名，但**不自动提交 Apple 公证**。正式发布的签名/公证流程见 [Desktop README 的 macOS signed DMG](README.md#macos-signed-dmg)。ad-hoc ZIP 用于本机/内部验证，不代表已经完成正式分发验证。

两种方式选一种即可。切换 ZIP/DMG、签名身份或重新构建后，需要使用新构建配套的 RAG ZIP；不要把前一次的组件默认当作新安装包的配套文件。

## 3. 找到安装包与本次 RAG 组件

| 文件/目录 | 用途 |
| --- | --- |
| `desktop/dist/mac-arm64/LazyMind.app` | 本次完整应用，可在本机直接启动 |
| `desktop/dist/LazyMind-darwin-arm64.zip` | 选择 ZIP 构建时的应用分发包 |
| `desktop/dist/LazyMind-macos-arm64.dmg` | 选择 DMG 构建时的应用分发包 |
| `desktop/dist/python-components/darwin-arm64/lazymind-python-rag-darwin-arm64-cp311-<revision>.zip` | **需要上传到 ModelScope 的 Mac RAG 组件** |
| 同目录的 `python-components.json`、`SHA256SUMS` | 配套清单和校验信息，留档即可 |

目录可能留有旧构建的 ZIP。以下命令对比最终 `.app` 与输出 catalog，并打印**本次唯一应该上传的文件名、默认 URL 和 SHA-256**：

```bash
node <<'NODE'
const fs = require('node:fs');
const dir = 'desktop/dist/python-components/darwin-arm64';
const app = JSON.parse(fs.readFileSync('desktop/dist/mac-arm64/LazyMind.app/Contents/Resources/runtime/config/python-components.json', 'utf8')).components.rag;
const out = JSON.parse(fs.readFileSync(`${dir}/python-components.json`, 'utf8')).components.rag;
for (const key of ['filename', 'revision', 'baseFingerprint', 'sha256', 'platform', 'arch', 'pythonAbi', 'url']) {
  if (app[key] !== out[key]) throw new Error(`安装包与组件不匹配：${key}`);
}
if (out.platform !== 'darwin' || out.arch !== 'arm64') throw new Error('不是 Mac ARM64 组件');
console.log(`上传文件：${dir}/${out.filename}`);
console.log(`下载地址：${out.url}`);
console.log(`大小：${(out.sizeBytes / 1024 / 1024).toFixed(2)} MiB`);
console.log(`SHA-256：${out.sha256}`);
NODE

(cd desktop/dist/python-components/darwin-arm64 && shasum -a 256 -c SHA256SUMS)
```

**安装包与组件必须来自同一次构建，且平台与架构相符。**

## 4. 上传前，验证本地组件

Mac 在 Electron 组装 `.app` 时才拆出 RAG；Developer ID 模式还会先签组件内的原生库。请使用**最终 `.app` 内的 Python 和 catalog**，不要用 `desktop/build/darwin-arm64/runtime` 中尚未拆包的中间环境，也不要手动再对已签名 `.app` 运行拆包脚本。

```bash
MAC_RUNTIME="$(pwd)/desktop/dist/mac-arm64/LazyMind.app/Contents/Resources/runtime"
MAC_PYTHON="$MAC_RUNTIME/deps/python/algorithm/bin/python"

"$MAC_PYTHON" -B desktop/scripts/verify-python-components.py \
  --runtime "$MAC_RUNTIME" \
  --bundle-dir "$(pwd)/desktop/dist/python-components/darwin-arm64" \
  --report "$(pwd)/desktop/dist/component-check/darwin-arm64/local-report.json"

# 验证导入没有向应用内写缓存、破坏签名
codesign --verify --deep --strict --verbose=2 desktop/dist/mac-arm64/LazyMind.app
```

成功应返回退出码 0，并且报告为 `"passed": true`。检查包括基础依赖导入、RAG ZIP 校验/解压、RAG 导入、Milvus 写入/落盘/重启后检索/删除集合。测试使用临时数据库，保留分阶段日志，不修改用户知识库。

若报错，先保留报告、日志和本次组件清单定位问题；以脚本退出码及生成报告判断本次校验结果。

## 5. 手动上传 ModelScope

1. 登录 ModelScope，打开第 3 步清单 URL 对应的数据集。
2. 将第 3 步打印的 **Mac RAG 原始 ZIP** 上传到该 URL 对应的分支和目录。
3. 保持文件名和 ZIP 内容不变，不解压后上传、不再次压缩，也不要把整个组件目录套成另一个 ZIP。
4. `python-components.json`、`SHA256SUMS` 和测试报告本地留档；应用运行不要求将它们上传。应用 ZIP/DMG 是给用户安装的另一个分发文件，不要把它填写为 RAG 下载地址。
5. 保留旧版本组件，供仍使用旧清单的客户端下载。

发布地址需允许客户端下载。上传后按第 6 节校验下载内容；发布目标或依赖改变时，重新生成并验证匹配的清单和安装包。

## 6. 验证刚上传的云端文件

继续使用第 4 步的两个变量，去掉 `--bundle-dir` 后，脚本会实际下载 catalog 中的 ModelScope URL：

```bash
"$MAC_PYTHON" -B desktop/scripts/verify-python-components.py \
  --runtime "$MAC_RUNTIME" \
  --report "$(pwd)/desktop/dist/component-check/darwin-arm64/cloud-report.json"

codesign --verify --deep --strict --verbose=2 desktop/dist/mac-arm64/LazyMind.app
```

再次确认退出码 0、`passed=true`。这一步验证云端文件与本次安装包清单相符，而不只是网页能打开；测试会下载完整组件。

## 7. 安装应用并按实际用户流程测试

先关闭已有 LazyMind Desktop/Local 实例，避免同一数据目录和端口冲突。ZIP 解压后将 `LazyMind.app` 放到“应用程序”；DMG 则打开后拖入“应用程序”。也可以先直接启动本次构建目录中的应用：

```bash
open desktop/dist/mac-arm64/LazyMind.app
```

按以下顺序检查：

1. 首次启动等待 warmup。Workflow 演示案例应自动下载、校验和导入；核对示例及图片/PPT 产物可打开。
2. 尚未安装 RAG 时，普通聊天、模型配置、常用附件功能应可使用；知识库相关操作应提示安装组件，而不是一直转圈。
3. 打开“设置 → 系统工具 → 依赖”，找到 RAG，确认默认地址是第 3 步打印的 Mac ZIP，点击安装。
4. 安装完成后点击“重启本地服务”，或退出后重新打开应用。重启前先结束正在进行的任务。
5. 新建测试知识库，上传 PDF/Word，等待解析完成，验证检索和知识问答；退出重开应用后再次检索同一份文档，并测试删除测试文档/知识库。
6. 回归普通聊天、常用 Workflow、Skill、Ark 图片/视频功能。已有同版本组件时可能直接显示已安装，不需要为了测试清除已有数据。

脚本通过只证明依赖与临时 Milvus 路径通过；界面、真实模型调用、首次 warmup、签名和安装行为仍以上述实际试用为准。

## 8. 常见问题与重新构建

| 现象 | 检查方式 |
| --- | --- |
| RAG 默认 URL 返回 404 | 对照第 3 步清单 URL，确认文件名、分支和目录正确且公开可读 |
| 下载后大小/SHA 或兼容性校验失败 | 上传对应 `.app` 同次构建的原始 ZIP；安装界面不提供自定义 URL |
| 没有生成 `darwin-arm64` 组件 | 检查构建是否完整成功、`LAZYMIND_DESKTOP_DEFER_PYTHON` 是否为 true，以及最终 `.app` 是否有 catalog |
| 验证脚本提示基础环境仍有 RAG | 检查使用的是最终 `.app` 的 Python，而不是中间 build runtime 或系统 Python |
| DMG 提示找不到签名身份 | 使用已配置 Developer ID 的钥匙串，或改用第 2 步的 ad-hoc ZIP 做本机测试 |
| macOS 提示来源/签名问题 | 区分本地 ad-hoc 包、Developer ID 签名包与已公证包；正式分发遵循现有签名/公证流程 |
| 构建拆包时弹出“应用已损坏”，随后文件消失 | 确认包含将 runtime 暂移到 `.app` 外拆包的修复；被移入废纸篓的构建产物需要重新封装 |
| 验证后签名报新增 `.nbc/.nbi` 文件 | 确认使用新版验证脚本，且 runtime-manager 设置 `NUMBA_CACHE_DIR` 到用户缓存；仅加 `-B` 不能阻止 Numba 缓存 |
| 修改代码后重新打包 | 重做第 3～6 步；按新 catalog 选择组件，不改写旧文件名来冒充新组件 |

GitHub 构建的安装包和 RAG 组件应取自同一次 Actions 运行，并按对应清单发布。

## 9. Intel Mac 构建

在 Intel 原生终端确认 `uname -m` 为 `x86_64`、`node -p process.arch` 为 `x64`，检出同一版本的主仓库和 LazyLLM 子模块。构建入口与 ARM64 共用脚本，但目标架构、原生依赖和组件清单独立。

```bash
LAZYMIND_RELEASE_BUILD=false LAZYMIND_DESKTOP_SIGNING_MODE=adhoc make desktop-darwin-x64
```

需要 Developer ID 签名 DMG 时，准备对应证书和公证配置后运行 `make desktop-darwin-x64-dmg`。构建名称为 `x64`，组件清单名称为 `amd64`；RAG ZIP、catalog 和 SHA 位于 `desktop/dist/python-components/darwin-amd64/`，实际文件名以生成的 `python-components.json` 为准。

应用和组件必须来自同一次原生构建，不能复用 ARM64 或 Windows 的 ZIP。保留配套 catalog、依赖锁和校验文件，再按第 4～6 节验证本地组件及其发布地址；使用 Intel 构建实际生成的应用路径和 Python，不要照搬 ARM64 路径。安装后检查登录、聊天、组件安装、文档入库及重启检索。

## 10. 资源发布与缓存

### Python 依赖共享

`LAZYMIND_DESKTOP_SHARE_PYTHON` 默认 `false`，仅审计共享候选；设置 `true` 才共享内容相同的纯 Python wheel。切换回不共享模式时执行完整构建，`resume` 不会恢复已移走的包。

共享清单位于最终 runtime 的 `config/python-sharing.json`。各环境通过相对 `.pth` 引用自己的共享目录，组件 catalog 继续绑定同一逻辑依赖集。

### PDF 字体

字体来源、许可证、大小和 SHA-256 由 `desktop/pdf-font.json` 维护，构建时写入 runtime 的 `config/pdf-font.json`。发布时上传清单对应的 TTF 原文件及许可证，并校验实际下载字节；不要将 ZIP 地址当作字体地址。

用户缓存位于 runtime 的 `deps/pdf-font/<sha>/`，校验通过后可离线复用，损坏缓存会重新下载。更新资源来源时重新生成配套清单和安装包，不直接修改已签名应用。

### 精选案例资源按需下载

远端 Skill ZIP 按需安装。构建以 `--catalog-only --frozen-lockfile` 生成预览目录；Skill 安装/使用请求才获取实际包。

精选图片/HTML 使用 `desktop/featured-assets.json` 维护案例 ZIP、MS 主源和 HF 备用源、ZIP 及逐文件 SHA-256。`stage-featured-assets.py` 校验编译素材与发布清单一致，生成小封面缩略图后移出已发布完整素材。已发布素材变化时构建仍失败，必须显式重新发布，不能静默使用旧包。桌面构建启用 `--bundle-unpublished`：上游新增且尚无发布条目的案例保留完整本地素材，并在 `downloads.json` 的 `local` 中记录逐文件 SHA/大小；不生成未发布 URL，也不改云端清单。新案例正式发布后自动恢复远程分包。

发布步骤：先用 `builtin-skill-bundle --catalog-only --frozen-lockfile` 生成完整 featured 目录，再运行 `stage-featured-assets.py <runtime> --publish <zip-output>`，上传输出 ZIP 到 HF 数据集 `featured-assets/` 目录，保留文件名，提交更新后的清单。正常构建不带 `--publish`，无需下载案例 ZIP。HTTP `/showcase-assets/` 在 desktop 通过 Core 提供缩略图或按案例缓存的资源；其他环境仍由原静态路由处理。

缓存位置：`<用户 runtime>/cache/featured-assets/<ZIP SHA-256>/`。断网时已有缓存和首页封面仍可使用；首次未缓存的详情资源下载失败后，恢复网络并刷新页面重试。图片和 HTML 仅作为案例展示素材分发，HTML 预览继续使用前端 sandbox iframe。

精选下载顺序：Chat 历史样例先下载并导入，再由 Core 后台预取首页 Chat 前 8 个精选、Work 前 8 个精选，最后按展示顺序下载其余可见案例。顺序与首页 placement.order 一致；后台预取不阻塞界面就绪。下载失败跳过，点击详情时自动重试；关闭应用取消下载，下次启动复用已校验缓存。
