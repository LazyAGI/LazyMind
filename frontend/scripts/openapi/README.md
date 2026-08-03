

本项目使用 OpenAPI Generator 自动生成前端 API 客户端代码。



当前前端生成链路的权威输入位于：
```
scripts/openapi/specs/auth-openapi.yaml
scripts/openapi/specs/core.yaml
scripts/openapi/specs/scan.yaml
scripts/openapi/specs/channel-gateway.yaml
```


生成的 TypeScript 接口代码位于：
```
src/api/generated/auth-client/
src/api/generated/core-client/
src/api/generated/scan-client/
src/api/generated/channel-gateway-client/
```



当您运行 `npm run dev` 时，会自动执行接口生成：

```bash
npm run dev
```

这个命令会：
1. 自动生成 `auth`、`core` 和 `scan` 接口（通过 `predev` 钩子）
2. 启动开发服务器


如果需要手动生成接口，可以使用以下命令：

```bash
npm run gen:auth

npm run gen:openapi auth
npm run gen:openapi core
npm run gen:openapi scan
npm run gen:openapi channel-gateway

npm run gen:openapi

npm run gen:openapi:check
```


- **Java 17+**: OpenAPI Generator 需要 Java 运行时，请在本机安装 JDK 17 或更高版本，并确保 `java` 在 `PATH` 中（例如设置 `JAVA_HOME` 并将 `$JAVA_HOME/bin` 加入 `PATH`）。


1. 将新的 OpenAPI YAML 文件放到 `scripts/openapi/specs/` 目录
2. 修改 `scripts/openapi/openapi-manifest.mjs`，添加新的 API 配置：

```javascript
const apis = [
  {
    name: "your-service",
    input: path.resolve(localSpecsDir, "your-service-openapi.yaml"),
    output: path.resolve(outputDirname, "your-service-client"),
  },
  // ... 其他配置
];
```

3. 生成接口：

```bash
npm run gen:openapi your-service
```


- `scripts/openapi/openapi-manifest.mjs`: OpenAPI 输入/输出清单
- `scripts/openapi/check-stale.mjs`: 检查 spec 与已生成 client 是否一致
- `scripts/openapi/generate-api.mjs`: 主生成脚本
- `scripts/openapi/generate-auth.sh`: 批量生成 auth、core、scan 接口（依赖系统 `PATH` 中的 `java`）
- `scripts/openapi/openapi-generator-config.json`: OpenAPI Generator 配置
- `scripts/openapi/.openapi-cache.json`: 缓存文件（避免重复生成）


生成脚本使用 SHA256 哈希来检测 OpenAPI 文件的变化。只有当文件内容改变时才会重新生成接口。

如果需要强制重新生成，可以：

```bash
node scripts/openapi/generate-api.mjs auth --skip-cache
```



如果遇到 "Unable to locate a Java Runtime" 错误：

1. 确认 Java 已安装且在 PATH 中：
```bash
java -version
```

2. 若未安装，请从发行版官网、包管理器或版本管理器安装 **JDK 17+**。例如 macOS 可用 Homebrew：`brew install openjdk@17`，然后将该 JDK 的 `bin` 目录加入 `PATH`（具体路径因安装方式而异）。

3. 若已安装但命令行找不到 `java`，请设置 `JAVA_HOME` 并将 `$JAVA_HOME/bin` 加入 `PATH`。


如果生成失败，请检查：
1. OpenAPI YAML 文件格式是否正确
2. Java 环境是否配置正确
3. 网络连接是否正常（首次运行需要下载 OpenAPI Generator）
4. 先执行 `npm run gen:openapi:check`，确认是否存在 stale 的 spec/client


当前行为说明：

- 如果本地没有 Java 且 spec 没变，`npm run dev` 会跳过生成并继续启动。
- 如果本地没有 Java 且 spec 已变，脚本会直接报错并阻止启动，避免继续使用过期 client。
- 如确需临时跳过 stale 检查，可使用 `OPENAPI_ALLOW_STALE=1 npm run dev`，但这只是临时兜底，不建议长期使用。


- **OpenAPI Generator CLI**: v2.20.2+
- **生成器**: typescript-axios
- **Axios 版本**: 1.6.8
