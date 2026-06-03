首先进入飞书开发平台https://open.feishu.cn/app?lang=zh-CN
成功进入后点击开发者后端按钮
https://internal-api-drive-stream.feishu.cn/space/api/box/stream/download/preview/IDdAbSG29oOuK2xpLZMcogfin2g/?preview_type=16
成功进入开发者后台后点击创建企业自建应用
https://internal-api-drive-stream.feishu.cn/space/api/box/stream/download/preview/I8hlbnnIgoR0JExwRBpcppzInCh/?preview_type=16
填写对应的应用名称和应用描述
https://internal-api-drive-stream.feishu.cn/space/api/box/stream/download/preview/CpA2becRDo6DptxQkzdc4qI8nje/?preview_type=16
创建之后，进入权限管理--开通权限，依次添加以下应用权限：
https://internal-api-drive-stream.feishu.cn/space/api/box/stream/download/preview/YCoKbmiv4oMqdRxJ5ZActCy9nie/?preview_type=16
方案一（通用版本）
        添加offline_access、drive、wiki、docx权限全部选择即可
方案二（细致权限版本）
"offline_access drive:drive drive:drive:readonly drive:drive.metadata:readonly wiki:wiki wiki:wiki:readonly wiki:node:retrieve docx:document"

offline_access
drive:drive 
drive:drive:readonly 
drive:drive.metadata:readonly 
wiki:wiki 
wiki:wiki:readonly 
wiki:node:retrieve 
docx:document"

添加权限后，点击版本管理与发布，并创建版本-填写版本信息-确认发布
https://internal-api-drive-stream.feishu.cn/space/api/box/stream/download/preview/YCoKbmiv4oMqdRxJ5ZActCy9nie/?preview_type=16
应用发布完成后点击进入安全设置：
将下面的链接添加至重定向URL
http://填写（前端应用）的ip和端口/oauth/feishu/data-source/callback 
https://internal-api-drive-stream.feishu.cn/space/api/box/stream/download/preview/ZE07bRPGIoHY0ixLCdTcjEHYnU3/?preview_type=16
完成后复制该应用的 App ID以及 App Secret至lazyRAG的数据源管理，点击“保存并选择飞书”
https://internal-api-drive-stream.feishu.cn/space/api/box/stream/download/preview/SjjybRTtOoAWMbxh2pYcbXounTb/?preview_type=16
https://internal-api-drive-stream.feishu.cn/space/api/box/stream/download/preview/V9kObMjpPoKX7Zx8s9ncGWgtnOc/?preview_type=16
在飞书云盘中创建文件夹，并将目录地址复制到lazyRAG：
https://internal-api-drive-stream.feishu.cn/space/api/box/stream/download/preview/Ti2bbPGMQoikomxozgkcSimFnQh/?preview_type=16
https://internal-api-drive-stream.feishu.cn/space/api/box/stream/download/preview/Xs9Tbtvoco7jv7xMbq1cohjxnXg/?preview_type=16
点击“连接账号”，授权后即可保存配置。
