# Zotero 导入

论文库的「导入 → 从 Zotero 导入」提供两个入口：**本地数据库**和 **API**。

## 本地数据库

点击「读取本地数据库」后，在列表中搜索、勾选需要的文献，再确认导入。只有勾选的文献及其 PDF、笔记、批注和所属文件夹层级会加入论文库。取消预览不会保存文献或上传文件。

本地运行时自动读取 Zotero 配置中的数据目录；默认目录为用户主目录下的 `Zotero`，数据库为 `zotero.sqlite`，附件位于 `storage`。自定义目录可以通过 `LAZYMIND_ZOTERO_DATA_DIR` 指定。桌面运行时使用 `LAZYMIND_HOST_HOME`，不会误读服务专用 HOME。

数据库以只读模式、同一个 SQLite 事务读取，包括尚未 checkpoint 的 WAL 数据。不复制一个可能过期的主数据库文件，不使用 `immutable=1`，也不修改 Zotero 的数据、设置或日志模式。Zotero 无需开启本地 API；无法读取或数据库不兼容时会显示具体错误。

默认仅从数据目录读取附件。链接附件如位于其他目录，可由运行环境设置 `LAZYMIND_ZOTERO_LINKED_ATTACHMENT_DIR`；该目录同样应只读挂载。旧电脑路径、未下载或已丢失的 PDF 会显示不可用，不能以成功附件计数。

### Docker

容器看不到宿主机文件，需要显式挂载 Zotero 数据目录，只允许文献库所有者访问：

```sh
export ZOTERO_DATA_DIR="$HOME/Zotero"
export LAZYMIND_ZOTERO_LOCAL_OWNER=admin
docker compose -f docker-compose.yml -f docker-compose.zotero.yml up -d --no-deps core
```

如果已有 `docker-compose.override.yml`，应把它作为额外的 `-f` 参数一同传入，保留已有配置。显式传入 `-f` 时不会自动加载默认 override。

`ZOTERO_DATA_DIR` 必须已存在；不会自动创建一个空目录。挂载为只读，且不挂载整个用户主目录。所有者是 LazyMind 用户名，服务会通过 Bearer token 向认证服务独立验证身份；拥有目标论文库写权限的其他用户不能读取宿主机 Zotero 数据。

远程服务器上的本地数据库入口读取的是服务器挂载的数据，不是浏览器所在电脑。访问其他电脑上的 Zotero，请使用 API 入口。

## API

API 入口保留本机 Zotero 客户端、远端 Zotero 和 WebDAV 配置：

- 本机客户端需保持 Zotero 运行，并在「设置 → 高级」允许其他应用与 Zotero 通讯；个人文献库 ID 为 `0`。
- 远端 Zotero 使用数字用户/群组 ID 和有读取权限的 API Key。
- WebDAV 提供附件，题录仍从 Zotero API 获取。

API 读取后同样先选择文献再确认。密钥和密码仅用于本次连接，不写入论文元数据或浏览器存储。导入中出现错误会显示具体失败；已确认保存的文献保留，附件可以继续重试。

参考：[Zotero 数据目录](https://www.zotero.org/support/zotero_data)、[Zotero SQLite 直接访问](https://www.zotero.org/support/dev/client_coding/direct_sqlite_database_access)、[SQLite WAL 只读访问](https://www.sqlite.org/wal.html#read_only_databases)。
