# 翻译源调研与接入说明

核对日期：2026-10-27。额度、开通条件与接口行为可能调整，请以链接中的服务商页面和自己的控制台为准。这里的“文档翻译”是 LazyMind 将文档内容分段后调用文本翻译接口，并非服务商提供的原生文档翻译产品。

| 来源 | 接入方式 | 调研时可免费使用的条件 | LazyMind 用途 |
| --- | --- | --- | --- |
| Google Translate Web | 免密钥网页接口 | 无公开额度承诺，可能限流或变更 | 划词、文档 |
| Tencent TranSmart Web | 免密钥网页接口 | 无公开额度承诺，可能限流或变更 | 划词、文档 |
| [MyMemory](https://mymemory.translated.net/doc/usagelimits.php) | 官方公共 API，无密钥 | 匿名请求共 5,000 字符/日；同一后端出口可能共享额度 | 仅中英短划词，单请求最多 500 字节，需指定源语种 |
| [百度翻译](https://fanyi-api.baidu.com/product/112) | APP ID + 密钥 | 标准版 5 万字符/月；个人认证高级版 100 万字符/月 | 划词、文档 |
| [阿里云机器翻译](https://help.aliyun.com/zh/machine-translation/product-overview/billing-overview) | AccessKey ID + Secret | 通用版 100 万字符/月；超额进入后付费 | 划词、文档 |
| [火山引擎机器翻译](https://docs.volcengine.com/docs/MachineTranslation/Productbilling?lang=zh) | AccessKey ID + Secret | 前 200 万字符/月；需要实名、开通及签约，超额后付费 | 划词、文档 |
| [Microsoft Translator](https://azure.microsoft.com/en-us/pricing/details/cognitive-services/translator/) | Azure 订阅 Key，可选 Region | F0 免费层 200 万字符/月 | 划词、文档 |
| [Google Cloud Translation](https://cloud.google.com/products/translate/pricing) | Cloud API Key | NMT 文本翻译前 50 万字符/月以每月抵扣形式免费；需项目和计费设置 | 划词、文档 |
| [小牛翻译](https://niutrans.com/documents/overview?id=3) | API Key | 注册账号每日 20 万字符 | 划词、文档 |
| [有道智云](https://ai.youdao.com/DOCSIRMA/html/trans/price/dmxfy/index.html) | 应用 ID + 密钥 | 新用户体验金，一次性；后续按服务规则收费 | 划词、文档 |
| [DeepL](https://support.deepl.com/hc/en-us/articles/360021200939-DeepL-API-plans) | API Key | 新用户 API Developer 总计 100 万字符的一次性试用；旧 API Free 已停售 | 划词、文档 |
| [彩云小译](https://docs.caiyunapp.com/lingocloud-api/index.html) | API Token | 新用户 100 万字、有效期一个月；超额计费 | 划词、文档 |
| [Tencent TokenHub](https://cloud.tencent.com/document/product/1823/130053) | TokenHub API Key | 本期新人活动每模型 100 万 Tokens，一年有效；活动暂定至 2026-12-31 | 划词、文档 |
| [腾讯云旧版机器翻译](https://cloud.tencent.com/document/product/551/104415) | SecretId + SecretKey | 仅兼容已有账号；文本接口不再接纳新用户，2026-10-01 起停止发放新免费资源包 | 划词、文档 |

Google Translate Web 和 Tencent TranSmart Web 使用网页接口，服务商未承诺第三方调用的稳定性。翻译内容会发送给所选服务商。MyMemory 的公共配额按服务端出口共享，适合偶尔翻译单句，不适合批量文档。

配置入口在“模型服务 → 翻译”。免密钥来源可以直接启用；其他来源需要在服务商侧开通，再填入自己的凭据。LazyMind 不代用户领取额度，也不会阻止服务商在免费额度用尽后按账户设置收费。使用按量计费服务时，请在服务商控制台设置额度或费用限制。

本轮也验证了 Bing、百度、有道等网页端请求。没有得到可重复、无需登录且无需绕过验证码的调用方式，因此未将其网页端伪装为可用翻译源。它们的正式开放 API 若具备免费额度，已按上表单独接入。

[Chrome 内置 Translator API](https://developer.chrome.com/docs/ai/translator-api) 和 [Edge 内置 Translator API](https://learn.microsoft.com/en-us/microsoft-edge/web-platform/translator-api) 可在支持的桌面浏览器内免费运行，但需要浏览器下载语言模型并在运行时检查可用性；它们不能作为当前后端文档任务的统一来源。[Apertium 公共 API](https://wiki.apertium.org/wiki/API) 支持免密钥调用，但目前没有本功能所需的中文语向。[LibreTranslate 公共镜像](https://docs.libretranslate.com/community/mirrors/) 无法确认可持续访问，自托管实例可以另行接入。Papago、Yandex、Reverso 未查到可持续使用的官方免密钥公网文本 API。
