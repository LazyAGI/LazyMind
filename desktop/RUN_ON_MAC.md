# 在 Apple Silicon Mac 上运行 LazyMind 内测版

## 适用范围

这个包只用于内部测试，当前只支持 Apple Silicon Mac。Intel Mac 暂不支持。

这个版本没有 Apple Developer ID 签名，也没有 Apple notarization。macOS 第一次启动时可能会拦截；只有在确认包来源可信时才继续打开。Apple 官方的放行说明见 [Open a Mac app from an unidentified developer](https://support.apple.com/en-us/102445)。

## 你会收到

- `LazyMind-darwin-arm64.zip`
- `SHA256SUMS.txt`

## 安装

1. 把两个文件放到同一个目录，例如 `~/Downloads`。
2. 可选但推荐：打开 Terminal 校验 zip 完整性。

   ```bash
   cd ~/Downloads
   shasum -a 256 -c SHA256SUMS.txt
   ```

   看到 `LazyMind-darwin-arm64.zip: OK` 后继续。

3. 双击解压 `LazyMind-darwin-arm64.zip`。
4. 把 `LazyMind.app` 拖到 `/Applications`。

## 首次启动

1. 双击 `/Applications/LazyMind.app`。
2. 如果 macOS 提示无法验证开发者或无法检查恶意软件，先点 `Done` 或关闭提示。
3. 打开 `System Settings` -> `Privacy & Security`，滚动到底部，点击 `Open Anyway`。
4. 再次确认 `Open`。之后通常可以直接双击启动。

## 终端兜底

仅在你确认包来源可信、并且 UI 放行仍失败时使用：

```bash
xattr -dr com.apple.quarantine /Applications/LazyMind.app
codesign --verify --deep --strict --verbose=2 /Applications/LazyMind.app
open /Applications/LazyMind.app
```

如果 `codesign --verify` 失败，请不要继续运行，把完整输出发给开发者。

## 启动和排障

首次启动可能需要几分钟，因为 LazyMind 会启动本地 runtime。启动页可以点：

- `Show startup log` 查看启动进度
- `Copy logs` 复制日志
- `Open logs` 打开日志目录

反馈问题时请提供这个文件：

```text
~/Library/Application Support/LazyMind/runtime/logs/desktop-startup.log
```

也可以提供整个日志目录：

```text
~/Library/Application Support/LazyMind/runtime/logs
```

## 卸载

退出 LazyMind 后删除：

```text
/Applications/LazyMind.app
```

如需清除本地测试数据，再删除：

```text
~/Library/Application Support/LazyMind
```
