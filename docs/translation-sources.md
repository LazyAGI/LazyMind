# 翻译源调研与接入说明

核对日期：2026-10-28。额度、开通条件与接口行为可能调整，请以服务商页面和自己的控制台为准。这里的“文档翻译”是 LazyMind 将文档内容分段后调用文本翻译接口，并非服务商提供的原生文档翻译产品。

## 当前可选来源

以下五个免密钥来源默认启用。它们使用网页或浏览器扩展接口，服务商未承诺第三方调用的稳定性；翻译内容会发送给所选服务商。

| 来源 | 接入方式 | LazyMind 用途 |
| --- | --- | --- |
| [谷歌翻译（网页）](https://translate.google.com/) | 免密钥网页接口 | 划词、文档 |
| [必应翻译（网页）](https://www.bing.com/translator) | 免密钥网页接口 | 划词、文档 |
| [搜狗翻译（网页）](https://fanyi.sogou.com/) | 免密钥网页接口 | 划词、文档 |
| [DeepL 网页翻译](https://www.deepl.com/translator) | 免密钥网页／浏览器扩展接口 | 划词、文档 |
| [腾讯翻译君（网页）](https://fanyi.qq.com/) | 免密钥网页接口 | 划词、文档 |

以下三个来源需要用户自己的凭据。免费额度可能取决于注册、认证及账户状态，超额后的计费以服务商规则为准。

| 来源 | 所需凭据 | LazyMind 用途 |
| --- | --- | --- |
| [百度翻译](https://fanyi-api.baidu.com/product/112) | APP ID + 密钥 | 划词、文档 |
| [阿里云机器翻译](https://help.aliyun.com/zh/machine-translation/product-overview/billing-overview) | AccessKey ID + Secret | 划词、文档 |
| [小牛翻译](https://niutrans.com/documents/overview?id=3) | API Key | 划词、文档 |

配置入口为“设置 → 默认模型 → 检索与工具 → 机器翻译”。在这里启用来源或保存凭据；在论文阅读页的翻译设置中选择默认机器翻译源。保存凭据不会自动改变默认来源。LazyMind 不代用户领取额度，也不会阻止服务商在免费额度用尽后按账户设置收费；使用按量计费服务时，请在服务商控制台设置费用限制。

## 暂停展示的调研来源

以下来源的实现和既有连接数据保留，当前版本的配置列表和选择菜单不展示它们。

| 来源 | 调研时的接入方式 |
| --- | --- |
| [火山引擎机器翻译](https://docs.volcengine.com/docs/MachineTranslation/Productbilling?lang=zh) | AccessKey ID + Secret |
| [Microsoft Translator](https://azure.microsoft.com/en-us/pricing/details/cognitive-services/translator/) | Azure 订阅 Key，可选 Region |
| [Google Cloud Translation](https://cloud.google.com/products/translate/pricing) | Cloud API Key，需项目和计费设置 |
| [有道智云](https://ai.youdao.com/DOCSIRMA/html/trans/price/dmxfy/index.html) | 应用 ID + 密钥 |
| [DeepL API](https://support.deepl.com/hc/en-us/articles/360021200939-DeepL-API-plans) | API Key |
| [彩云小译](https://docs.caiyunapp.com/lingocloud-api/index.html) | API Token |
| [Tencent TokenHub](https://cloud.tencent.com/document/product/1823/130053) | TokenHub API Key |
| [腾讯云旧版机器翻译](https://cloud.tencent.com/document/product/551/104415) | SecretId + SecretKey |

本轮也验证了百度、有道等网页端请求。没有得到可重复、无需登录且无需绕过验证码的调用方式，因此未将其网页端伪装为可用翻译源。

[Chrome 内置 Translator API](https://developer.chrome.com/docs/ai/translator-api) 和 [Edge 内置 Translator API](https://learn.microsoft.com/en-us/microsoft-edge/web-platform/translator-api) 可在支持的桌面浏览器内运行，但需要浏览器下载语言模型并在运行时检查可用性；它们不能作为当前后端文档任务的统一来源。[Apertium 公共 API](https://wiki.apertium.org/wiki/API) 目前没有本功能所需的中文语向。[LibreTranslate 公共镜像](https://docs.libretranslate.com/community/mirrors/) 无法确认可持续访问，自托管实例可以另行接入。Papago、Yandex、Reverso 未查到可持续使用的官方免密钥公网文本 API。
