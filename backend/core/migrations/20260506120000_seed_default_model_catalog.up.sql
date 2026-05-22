-- +migrate Up

-- Seed default_model_providers from lazy模型汇总表_副本.xlsx (name, description, base_url).
-- id: 32-char hex, same format as backend/core/common.GenerateID() (UUID v4).

INSERT INTO "default_model_providers" ("id", "name", "description", "base_url", "created_at", "updated_at", "deleted_at") VALUES
  ('c4c41f0440c64c1dae6a41e7cf3d445b', 'Claude', $d0$Anthropic 打造的顶尖 AI 基座，具备强大的自适应思考能力与原生视觉支持，在代码开发与复杂 Agent 任务上业界领先。

获取 API Key：
https://console.anthropic.com/settings/keys

申请教程：

1. 访问 Anthropic Console，使用海外邮箱或 Google 账号直接注册。

2. 登录后（需验证海外手机号并绑定外币信用卡结算），进入左侧菜单的 Settings → API Keys 页面。

3. 点击 Create Key，输入辨识名称后即可生成，请务必当场复制并妥善保存（关闭弹窗后无法再次查看完整 Key）。$d0$, 'https://api.anthropic.com/v1/', now(), now(), NULL),
  ('eadef5c69d2a4496809861634fe340b7', 'DeepSeek', $d1$国产顶尖大模型，推理模型性价比极高，支持深度推理与长链思考输出。

获取 API Key：
https://platform.deepseek.com/api_keys

申请教程：

1. 访问 DeepSeek 开放平台，使用手机号或邮箱完成注册。

2. 登录后进入左侧导航栏的 API Keys 页面，点击"创建 API Key"。

3. 为 Key 命名后点击创建，系统会生成一串密钥，请复制并妥善保存至你的代码配置中。$d1$, 'https://api.deepseek.com/', now(), now(), NULL),
  ('3d6fe37fbe514ca7b2aedec309a6abd4', 'Doubao', $d2$字节跳动火山引擎出品，旗舰模型深度思考能力极强，全面覆盖复杂推理、代码开发及高精度文生图等多模态场景。

获取 API Key 指南：

访问 火山方舟

一站式大模型服务平台 https://www.volcengine.com/product/ark，点击 【立即体验】 并登录。

点击页面右上角的 【控制台】。

在左侧导航栏中找到并点击 【API-Key 管理】 即可进行配置与创建。$d2$, 'https://ark.cn-beijing.volces.com/api/v3/', now(), now(), NULL),
  ('8b2d73f125c44725882d08e348185acc', 'GLM', $d3$清华出身的国产大模型，Coding 能力已对齐顶尖闭源模型，深度适配 Agent 工作流、工具调用与超长上下文解析。

获取 API Key：
https://open.bigmodel.cn/usercenter/apikeys

申请教程：

1. 访问智谱 AI 开放平台，使用手机号或微信扫码注册并实名。

2. 登录后进入右上角的"控制台"，在左侧菜单选择"API Keys"。

3. 点击"添加新的 API Key"，设置便于区分的名称，生成后复制完整的 Key 即可使用。$d3$, 'https://open.bigmodel.cn/api/paas/v4/', now(), now(), NULL),
  ('2714c5f4af594f23a1fddac3153bbb95', 'Kimi', $d4$Moonshot AI 出品，支持超长上下文推理，Agentic Coding 与长周期执行能力出众，中英文处理极佳。

获取 API Key：
https://platform.moonshot.cn/console/api-keys

申请教程：

1. 访问 Kimi 开放平台（Moonshot 开发者中心），使用手机号验证注册。

2. 登录后在左侧导航栏选择"API Key 管理"页面。

3. 点击"新建 API Key"，输入名称后点击生成，请当场复制这段密钥并安全保存。$d4$, 'https://api.moonshot.cn/', now(), now(), NULL),
  ('d647ba631e6c439bbfb968ff84b1aac5', 'Minimax', $d5$MiniMax 自研的通用大模型，开启模型自我迭代，在文本对话、超拟人语音合成及视频生成领域稳居第一梯队。

获取 API Key：
https://platform.minimaxi.com/

申请教程：

1. 访问 MiniMax 开放平台主页，点击右上角登录/注册开发者账号。

2. 登录后进入后台工作台，在左侧导航栏找到 "账户管理"，点击展开后选择 "接口密钥"（或者"订阅管理"下的 Token Plan）。

3. 点击"创建新的 API Key"，设置名称并确认后，复制生成的 API Key 用于接口调用（请注意按量付费 Key 和 Token Plan Key 不互通）。$d5$, 'https://api.minimaxi.com/v1/', now(), now(), NULL),
  ('e93bdc713dd14f16a9a6a9b282ad7d1d', 'OpenAI', $d6$AI 行业绝对标杆，引领智能前沿，在复杂编码、专业工作流与子代理生态上最为完善。

获取 API Key：
https://platform.openai.com/api-keys

申请教程：

1. 访问 OpenAI Platform，使用邮箱或 Google 账号注册（需具备海外网络环境及海外手机号验证）。

2. 在左侧 Dashboard 菜单中选择"API keys"选项卡（需先在 Billing 中绑定海外信用卡并充值）。

3. 点击"Create new secret key"，为 Key 命名并配置权限，点击生成后立刻复制（该密钥仅显示一次）。$d6$, 'https://api.openai.com/v1/', now(), now(), NULL),
  ('7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', $d7$阿里云开源与闭源双轨并行的王牌模型，全家桶覆盖文本、视觉、语音及图像编辑，国内生态最为丰富，API 调用极其便捷。

获取 API Key：
https://bailian.console.aliyun.com/?apiKey=1#/api-key

申请教程：

1. 访问阿里云百炼控制台，使用阿里云账号（支持支付宝/钉钉扫码）登录并开通百炼服务。

2. 在控制台右上角点击头像，进入"API-KEY"管理页面。

3. 点击"创建 API-KEY"，系统会立即生成一串鉴权密钥，点击复制即可在应用中使用。$d7$, 'https://dashscope.aliyuncs.com/', now(), now(), NULL),
  ('72ebcc11418d432887c28145b1600722', 'SenseNova', $d8$商汤科技打造的国产重磅基座，深度推理与多模态理解能力出色，综合性能强悍。

获取 API Key：
https://console.sensecore.cn/cn-sh-01/aistudio/management/api-key

申请教程：

1. 访问商汤大模型开放平台（SenseCore大装置），注册并完成企业或个人开发者认证。

2. 登录控制台，在左侧导航栏找到"API-Key管理"。

3. 点击"创建API-Key"，自定义名称后即可生成，复制该 API Key 即可进行模型调用。$d8$, 'https://api.sensenova.cn/compatible-mode/v1/', now(), now(), NULL),
  ('f756580d5758487ea88691268169308c', 'SiliconFlow', $d9$极致高效的模型 API 聚合加速平台，提供一站式、高并发调用服务，价格极其优惠。

获取 API Key：
https://cloud.siliconflow.cn/account/ak

申请教程：

1. 访问 SiliconFlow 云平台，使用手机号、微信或邮箱注册账号。

2. 登录控制台，在左侧菜单栏中点击"API 密钥"选项。

3. 点击"新建 API 密钥"，输入自定义名称描述，生成后一键复制，即可轻松接入各大开源旗舰模型。$d9$, 'https://api.siliconflow.cn/v1/', now(), now(), NULL);

-- Seed default_models from lazy模型汇总表_副本2.xlsx.
-- default_model_provider_id maps 供应商名 to id in 20260506120000_seed_default_model_providers.up.sql.
-- id: 32-char hex, same format as backend/core/common.GenerateID().

INSERT INTO "default_models" ("id", "default_model_provider_id", "provider_name", "name", "model_type", "base_url", "created_at", "updated_at", "deleted_at") VALUES
  ('63f8bbe57cb542d5b12ae9698d73b5e1', 'c4c41f0440c64c1dae6a41e7cf3d445b', 'Claude', 'claude-haiku-4-5', 'VLM', 'https://api.anthropic.com/v1/', now(), now(), NULL),
  ('33590c1c64db4915a231bc8101147b2b', 'c4c41f0440c64c1dae6a41e7cf3d445b', 'Claude', 'claude-opus-4-7', 'VLM', 'https://api.anthropic.com/v1/', now(), now(), NULL),
  ('96ecfbad967f4da38757e5da560bdec8', 'c4c41f0440c64c1dae6a41e7cf3d445b', 'Claude', 'claude-sonnet-4-6', 'VLM', 'https://api.anthropic.com/v1/', now(), now(), NULL),
  ('d7c32e56676f479881cba4a096096fc9', 'eadef5c69d2a4496809861634fe340b7', 'DeepSeek', 'deepseek-v4-flash', 'llm', 'https://api.deepseek.com', now(), now(), NULL),
  ('94311f7b03594cee8181ced62c761c64', 'eadef5c69d2a4496809861634fe340b7', 'DeepSeek', 'deepseek-v4-pro', 'llm', 'https://api.deepseek.com', now(), now(), NULL),
  ('bd62aa34bf04408b846f315fb1638c2e', '3d6fe37fbe514ca7b2aedec309a6abd4', 'Doubao', 'deepseek-r1-250528', 'llm', 'https://ark.cn-beijing.volces.com/api/v3/', now(), now(), NULL),
  ('326179288ff049378d03f1b31b034a2c', '3d6fe37fbe514ca7b2aedec309a6abd4', 'Doubao', 'deepseek-v3-1-terminus', 'llm', 'https://ark.cn-beijing.volces.com/api/v3/', now(), now(), NULL),
  ('333095a8f52f4a63bca04ed712e644ff', '3d6fe37fbe514ca7b2aedec309a6abd4', 'Doubao', 'deepseek-v3-2-251201', 'llm', 'https://ark.cn-beijing.volces.com/api/v3/', now(), now(), NULL),
  ('9cca86190b9c4c4299f3684b0525f7e1', '3d6fe37fbe514ca7b2aedec309a6abd4', 'Doubao', 'deepseek-v3-250324', 'llm', 'https://ark.cn-beijing.volces.com/api/v3/', now(), now(), NULL),
  ('8c423f9b9e2d432caddfc7bae7019a67', '3d6fe37fbe514ca7b2aedec309a6abd4', 'Doubao', 'doubao-1-5-lite-32k-250115', 'llm', 'https://ark.cn-beijing.volces.com/api/v3/', now(), now(), NULL),
  ('291191006aaa4dbc8b6e1d10f1cbc559', '3d6fe37fbe514ca7b2aedec309a6abd4', 'Doubao', 'doubao-1-5-pro-32k-250115', 'llm', 'https://ark.cn-beijing.volces.com/api/v3/', now(), now(), NULL),
  ('0d1ef7533d96454a8c26095cde985926', '3d6fe37fbe514ca7b2aedec309a6abd4', 'Doubao', 'doubao-1-5-pro-32k-character-250228', 'llm', 'https://ark.cn-beijing.volces.com/api/v3/', now(), now(), NULL),
  ('2026ad0e2e35429ba70f51ebd34bd1d6', '3d6fe37fbe514ca7b2aedec309a6abd4', 'Doubao', 'doubao-1-5-pro-32k-character-250715', 'llm', 'https://ark.cn-beijing.volces.com/api/v3/', now(), now(), NULL),
  ('dce59fb09a4249baa5f8ca1c3b6f1d27', '3d6fe37fbe514ca7b2aedec309a6abd4', 'Doubao', 'doubao-1-5-vision-pro-32k-250115', 'VLM', 'https://ark.cn-beijing.volces.com/api/v3/', now(), now(), NULL),
  ('96f18c6ed1d04f88b90283737f65ad1c', '3d6fe37fbe514ca7b2aedec309a6abd4', 'Doubao', 'doubao-embedding-text-240715', 'embedding', 'https://ark.cn-beijing.volces.com/api/v3/embeddings', now(), now(), NULL),
  ('feb3e79e2c764f5aa9c533805bd3aa09', '3d6fe37fbe514ca7b2aedec309a6abd4', 'Doubao', 'doubao-embedding-vision-241215', 'multimodal_embedding', 'https://ark.cn-beijing.volces.com/api/v3/embeddings/multimodal', now(), now(), NULL),
  ('0962c15db7ea444dbdee0490687f2398', '3d6fe37fbe514ca7b2aedec309a6abd4', 'Doubao', 'doubao-seed-1-6-250615', 'llm', 'https://ark.cn-beijing.volces.com/api/v3/', now(), now(), NULL),
  ('ed9c108462f841d1a66be9e47a6b1e2a', '3d6fe37fbe514ca7b2aedec309a6abd4', 'Doubao', 'doubao-seed-1-6-251015', 'llm', 'https://ark.cn-beijing.volces.com/api/v3/', now(), now(), NULL),
  ('698667fcc2414de297a87b99dc42850b', '3d6fe37fbe514ca7b2aedec309a6abd4', 'Doubao', 'doubao-seed-1-6-flash-250828', 'llm', 'https://ark.cn-beijing.volces.com/api/v3/', now(), now(), NULL),
  ('01f11b9a979148748061f80bd9e16b0f', '3d6fe37fbe514ca7b2aedec309a6abd4', 'Doubao', 'doubao-seed-1-6-lite-251015', 'llm', 'https://ark.cn-beijing.volces.com/api/v3/', now(), now(), NULL),
  ('c9775b3bafc94c1bb57b19d26a830b76', '3d6fe37fbe514ca7b2aedec309a6abd4', 'Doubao', 'doubao-seed-1-6-vision-250815', 'VLM', 'https://ark.cn-beijing.volces.com/api/v3/', now(), now(), NULL),
  ('789181e1168e4cd9a1ee044fc03a0ad2', '3d6fe37fbe514ca7b2aedec309a6abd4', 'Doubao', 'doubao-seed-1-8-251228', 'llm', 'https://ark.cn-beijing.volces.com/api/v3/', now(), now(), NULL),
  ('5764db7061ad4f6fa4c9b24c12bb2d12', '3d6fe37fbe514ca7b2aedec309a6abd4', 'Doubao', 'doubao-seed-2-0-code-preview-260215', 'llm', 'https://ark.cn-beijing.volces.com/api/v3/', now(), now(), NULL),
  ('598b7b234c0f48cbba586fe646128ec6', '3d6fe37fbe514ca7b2aedec309a6abd4', 'Doubao', 'doubao-seed-2-0-lite-260215', 'llm', 'https://ark.cn-beijing.volces.com/api/v3/', now(), now(), NULL),
  ('9df2a31c3d034644a917f8f863f2ee91', '3d6fe37fbe514ca7b2aedec309a6abd4', 'Doubao', 'doubao-seed-2-0-mini-260215', 'llm', 'https://ark.cn-beijing.volces.com/api/v3/', now(), now(), NULL),
  ('616d86523b164335b0a7cba83879b939', '3d6fe37fbe514ca7b2aedec309a6abd4', 'Doubao', 'doubao-seed-2-0-pro-260215', 'llm', 'https://ark.cn-beijing.volces.com/api/v3/', now(), now(), NULL),
  ('ed5f69e9f62848388e43f20acad1596a', '3d6fe37fbe514ca7b2aedec309a6abd4', 'Doubao', 'doubao-seed-code-preview-251028', 'llm', 'https://ark.cn-beijing.volces.com/api/v3/', now(), now(), NULL),
  ('618be35bdd2743d88ddf66a6f7ee3818', '3d6fe37fbe514ca7b2aedec309a6abd4', 'Doubao', 'doubao-seedream-3-0-t2i-250415', 'text2image', 'https://ark.cn-beijing.volces.com/api/v3', now(), now(), NULL),
  ('3e2a672d89fa4ffe9ef528c1d9465faa', '3d6fe37fbe514ca7b2aedec309a6abd4', 'Doubao', 'doubao-seedream-5-0', 'text2image', 'https://ark.cn-beijing.volces.com/api/v3', now(), now(), NULL),
  ('cfc2d701c3b54ed0af27d11c20f8519a', '3d6fe37fbe514ca7b2aedec309a6abd4', 'Doubao', 'glm-4-7-251222', 'llm', 'https://ark.cn-beijing.volces.com/api/v3/', now(), now(), NULL),
  ('eaf3694257734071aed82446ea01ff0c', '8b2d73f125c44725882d08e348185acc', 'GLM', 'AutoGLM-Phone', 'VLM', 'https://open.bigmodel.cn/api/paas/v4/', now(), now(), NULL),
  ('c8dc34542f9c487b9f8cc8392ed8037f', '8b2d73f125c44725882d08e348185acc', 'GLM', 'CharGLM-4', 'llm', 'https://open.bigmodel.cn/api/paas/v4/', now(), now(), NULL),
  ('22427cd64412412283541ceff4d90063', '8b2d73f125c44725882d08e348185acc', 'GLM', 'CodeGeeX-4', 'llm', 'https://open.bigmodel.cn/api/paas/v4/', now(), now(), NULL),
  ('17f88e05a51149a6a3812738acab0444', '8b2d73f125c44725882d08e348185acc', 'GLM', 'CogView-3-Flash', 'text2image', 'https://open.bigmodel.cn/api/paas/v4', now(), now(), NULL),
  ('59f623715fa4415793345e4919c16a00', '8b2d73f125c44725882d08e348185acc', 'GLM', 'CogView-4', 'text2image', 'https://open.bigmodel.cn/api/paas/v4', now(), now(), NULL),
  ('01dcc0f834dd47ec8123a72ee8eb7b69', '8b2d73f125c44725882d08e348185acc', 'GLM', 'Embedding-2', 'embedding', 'https://open.bigmodel.cn/api/paas/v4/embeddings', now(), now(), NULL),
  ('141251e696a84a29a2cda919b0c0ea3a', '8b2d73f125c44725882d08e348185acc', 'GLM', 'Embedding-3', 'embedding', 'https://open.bigmodel.cn/api/paas/v4/embeddings', now(), now(), NULL),
  ('9d0055bff97342f980972efcf30edaf5', '8b2d73f125c44725882d08e348185acc', 'GLM', 'GLM-4-Flash-250414', 'llm', 'https://open.bigmodel.cn/api/paas/v4/', now(), now(), NULL),
  ('2ec9acd2f33a42d68093777c568cfa88', '8b2d73f125c44725882d08e348185acc', 'GLM', 'GLM-4-FlashX-250414', 'llm', 'https://open.bigmodel.cn/api/paas/v4/', now(), now(), NULL),
  ('8577cb2d2f8c4fe3ad41df015511b47d', '8b2d73f125c44725882d08e348185acc', 'GLM', 'GLM-4-Long', 'llm', 'https://open.bigmodel.cn/api/paas/v4/', now(), now(), NULL),
  ('e5c412604b1444df92f24bd04a3b4c7a', '8b2d73f125c44725882d08e348185acc', 'GLM', 'GLM-4-Voice', 'tts', 'https://open.bigmodel.cn/api/paas/v4', now(), now(), NULL),
  ('96061759cc0a4a819c3ec327f70d6327', '8b2d73f125c44725882d08e348185acc', 'GLM', 'GLM-4.1V-Thinking-Flash', 'llm', 'https://open.bigmodel.cn/api/paas/v4/', now(), now(), NULL),
  ('6558ed680ccd45a59c7dafe2e5a3cfd0', '8b2d73f125c44725882d08e348185acc', 'GLM', 'GLM-4.1V-Thinking-FlashX', 'llm', 'https://open.bigmodel.cn/api/paas/v4/', now(), now(), NULL),
  ('3586e5c63c924a1cbf74c77ceb07adc6', '8b2d73f125c44725882d08e348185acc', 'GLM', 'GLM-4.5-Air', 'llm', 'https://open.bigmodel.cn/api/paas/v4/', now(), now(), NULL),
  ('dd272c8407f34b94bd0d9f459532baec', '8b2d73f125c44725882d08e348185acc', 'GLM', 'GLM-4.5-AirX', 'llm', 'https://open.bigmodel.cn/api/paas/v4/', now(), now(), NULL),
  ('9f2304a13c014426b50b97f11f20cbb2', '8b2d73f125c44725882d08e348185acc', 'GLM', 'GLM-4.5-Flash', 'llm', 'https://open.bigmodel.cn/api/paas/v4/', now(), now(), NULL),
  ('5f835f82370e4fefb7d9a0d8b2f7f9da', '8b2d73f125c44725882d08e348185acc', 'GLM', 'GLM-4.6', 'llm', 'https://open.bigmodel.cn/api/paas/v4/', now(), now(), NULL),
  ('c44202fbd9a34cbf97540181d2435a11', '8b2d73f125c44725882d08e348185acc', 'GLM', 'GLM-4.6V', 'VLM', 'https://open.bigmodel.cn/api/paas/v4/', now(), now(), NULL),
  ('acbd8d5098f244d292fbb8bf165640e7', '8b2d73f125c44725882d08e348185acc', 'GLM', 'GLM-4.6V-Flash', 'VLM', 'https://open.bigmodel.cn/api/paas/v4/', now(), now(), NULL),
  ('db9376e87e904e3bb1c63893d3306b5c', '8b2d73f125c44725882d08e348185acc', 'GLM', 'GLM-4.7', 'llm', 'https://open.bigmodel.cn/api/paas/v4/', now(), now(), NULL),
  ('393539ec76314e2597724ecf54ccb1d4', '8b2d73f125c44725882d08e348185acc', 'GLM', 'GLM-4.7-Flash', 'llm', 'https://open.bigmodel.cn/api/paas/v4/', now(), now(), NULL),
  ('6f7cfbae7350493bb1252f89ab9d6cd0', '8b2d73f125c44725882d08e348185acc', 'GLM', 'GLM-4.7-FlashX', 'llm', 'https://open.bigmodel.cn/api/paas/v4/', now(), now(), NULL),
  ('b1598ff5f6a94397a6f65ca369b68ad9', '8b2d73f125c44725882d08e348185acc', 'GLM', 'GLM-4V-Flash', 'VLM', 'https://open.bigmodel.cn/api/paas/v4/', now(), now(), NULL),
  ('2cedf624fd974f5ea0bf118df2202e5c', '8b2d73f125c44725882d08e348185acc', 'GLM', 'GLM-5', 'llm', 'https://open.bigmodel.cn/api/paas/v4/', now(), now(), NULL),
  ('5f998f01dae048a9a0dd81c20af6912f', '8b2d73f125c44725882d08e348185acc', 'GLM', 'GLM-5-Turbo', 'llm', 'https://open.bigmodel.cn/api/paas/v4/', now(), now(), NULL),
  ('7b971cb9e7374877a323122256dcb221', '8b2d73f125c44725882d08e348185acc', 'GLM', 'GLM-5.1', 'llm', 'https://open.bigmodel.cn/api/paas/v4/', now(), now(), NULL),
  ('9e167f95315f4f39bcecfcab3ba142e1', '8b2d73f125c44725882d08e348185acc', 'GLM', 'GLM-5V-Turbo', 'VLM', 'https://open.bigmodel.cn/api/paas/v4/', now(), now(), NULL),
  ('4afeef38e9524111a91daf893056e621', '8b2d73f125c44725882d08e348185acc', 'GLM', 'GLM-ASR-2512', 'stt', 'https://open.bigmodel.cn/api/paas/v4', now(), now(), NULL),
  ('def8a1794d0947d3aa0179c2d2283fa5', '8b2d73f125c44725882d08e348185acc', 'GLM', 'GLM-Image', 'text2image', 'https://open.bigmodel.cn/api/paas/v4', now(), now(), NULL),
  ('d2fb906c1d2d422eb289b0c8c966cc9c', '8b2d73f125c44725882d08e348185acc', 'GLM', 'GLM-TTS', 'tts', 'https://open.bigmodel.cn/api/paas/v4', now(), now(), NULL),
  ('56c25eeeb4d84026830da9c775ffabfb', '8b2d73f125c44725882d08e348185acc', 'GLM', 'GLM-TTS-Clone', 'tts', 'https://open.bigmodel.cn/api/paas/v4', now(), now(), NULL),
  ('472f2a0cac3f49d7a1f44c802113b1f9', '8b2d73f125c44725882d08e348185acc', 'GLM', 'Rerank', 'rerank', 'https://open.bigmodel.cn/api/paas/v4/rerank', now(), now(), NULL),
  ('362c2ac23823460b97921843ec8a582c', '2714c5f4af594f23a1fddac3153bbb95', 'Kimi', 'kimi-k2-0711-preview', 'llm', 'https://api.moonshot.cn/', now(), now(), NULL),
  ('fb9dd64bad57499cb677d0d7144b880a', '2714c5f4af594f23a1fddac3153bbb95', 'Kimi', 'kimi-k2-0905-preview', 'llm', 'https://api.moonshot.cn/', now(), now(), NULL),
  ('34819c1f6bdf4586b3eb059cc3506919', '2714c5f4af594f23a1fddac3153bbb95', 'Kimi', 'kimi-k2-thinking', 'llm', 'https://api.moonshot.cn/', now(), now(), NULL),
  ('cd0105a92178456b830ccb897cc51a2f', '2714c5f4af594f23a1fddac3153bbb95', 'Kimi', 'kimi-k2-thinking-turbo', 'llm', 'https://api.moonshot.cn/', now(), now(), NULL),
  ('f48d505247f241618ef42de630fc8044', '2714c5f4af594f23a1fddac3153bbb95', 'Kimi', 'kimi-k2-turbo-preview', 'llm', 'https://api.moonshot.cn/', now(), now(), NULL),
  ('035682fe6aaf48cc82b738ff84323d43', '2714c5f4af594f23a1fddac3153bbb95', 'Kimi', 'kimi-k2.5', 'VLM', 'https://api.moonshot.cn/', now(), now(), NULL),
  ('0eb729fac8a64adda6d109684954a772', '2714c5f4af594f23a1fddac3153bbb95', 'Kimi', 'kimi-k2.6', 'VLM', 'https://api.moonshot.cn/', now(), now(), NULL),
  ('6dab3e05f37d4f4bae2cc5f6e8f678a4', '2714c5f4af594f23a1fddac3153bbb95', 'Kimi', 'moonshot-v1-128k', 'llm', 'https://api.moonshot.cn/', now(), now(), NULL),
  ('5a88ca618c6a493287dcf1f4be169683', '2714c5f4af594f23a1fddac3153bbb95', 'Kimi', 'moonshot-v1-128k-vision-preview', 'VLM', 'https://api.moonshot.cn/', now(), now(), NULL),
  ('6117d953461643bc8480259142af246b', '2714c5f4af594f23a1fddac3153bbb95', 'Kimi', 'moonshot-v1-32k', 'llm', 'https://api.moonshot.cn/', now(), now(), NULL),
  ('7608d91184ab4899a6016ebb2f2b516f', '2714c5f4af594f23a1fddac3153bbb95', 'Kimi', 'moonshot-v1-32k-vision-preview', 'VLM', 'https://api.moonshot.cn/', now(), now(), NULL),
  ('2eda8f174fd84b9ba6e5b39823b86813', '2714c5f4af594f23a1fddac3153bbb95', 'Kimi', 'moonshot-v1-8k', 'llm', 'https://api.moonshot.cn/', now(), now(), NULL),
  ('f4b9df89c76b4c0c88f8f7927927e69f', '2714c5f4af594f23a1fddac3153bbb95', 'Kimi', 'moonshot-v1-8k-vision-preview', 'VLM', 'https://api.moonshot.cn/', now(), now(), NULL),
  ('7242a5add15c4653b935f6765b8b0ca2', 'd647ba631e6c439bbfb968ff84b1aac5', 'Minimax', 'M2-her', 'llm', 'https://api.minimaxi.com/v1/', now(), now(), NULL),
  ('3637ef0549fd41dc94230b364dfa8f27', 'd647ba631e6c439bbfb968ff84b1aac5', 'Minimax', 'MiniMax-M2.5', 'llm', 'https://api.minimaxi.com/v1/', now(), now(), NULL),
  ('983ffb5350ea4d778b04f58affef0659', 'd647ba631e6c439bbfb968ff84b1aac5', 'Minimax', 'MiniMax-M2.5-highspeed', 'llm', 'https://api.minimaxi.com/v1/', now(), now(), NULL),
  ('145d2980fd264b499092baed74742ebb', 'd647ba631e6c439bbfb968ff84b1aac5', 'Minimax', 'MiniMax-M2.7', 'llm', 'https://api.minimaxi.com/v1/', now(), now(), NULL),
  ('d75c1134c1a24bbebf3cb2b89b47a728', 'd647ba631e6c439bbfb968ff84b1aac5', 'Minimax', 'MiniMax-M2.7-highspeed', 'llm', 'https://api.minimaxi.com/v1/', now(), now(), NULL),
  ('b12c59dc2f674362ab8ef6b4da10cca2', 'd647ba631e6c439bbfb968ff84b1aac5', 'Minimax', 'Speech-02-HD', 'tts', 'https://api.minimaxi.com/v1/', now(), now(), NULL),
  ('0d31893f5aa34928ba5aabe4231a8bee', 'd647ba631e6c439bbfb968ff84b1aac5', 'Minimax', 'Speech-02-Turbo', 'tts', 'https://api.minimaxi.com/v1/', now(), now(), NULL),
  ('a0cad0c849e5443e9a78c753f78ba9d4', 'd647ba631e6c439bbfb968ff84b1aac5', 'Minimax', 'Speech-2.6-HD', 'tts', 'https://api.minimaxi.com/v1/', now(), now(), NULL),
  ('48f563dee5e24b6fabe9768a1241e743', 'd647ba631e6c439bbfb968ff84b1aac5', 'Minimax', 'Speech-2.6-Turbo', 'tts', 'https://api.minimaxi.com/v1/', now(), now(), NULL),
  ('c3bbd3d67d944baab4fa2b483f6b2dce', 'd647ba631e6c439bbfb968ff84b1aac5', 'Minimax', 'Speech-2.8-HD', 'tts', 'https://api.minimaxi.com/v1/', now(), now(), NULL),
  ('05845f71a4d54eae944dc05e1f8bd6fa', 'd647ba631e6c439bbfb968ff84b1aac5', 'Minimax', 'Speech-2.8-Turbo', 'tts', 'https://api.minimaxi.com/v1/', now(), now(), NULL),
  ('3f14593b5450483382bbff15e076f0f9', 'd647ba631e6c439bbfb968ff84b1aac5', 'Minimax', 'image-01', 'text2image', 'https://api.minimaxi.com/v1/', now(), now(), NULL),
  ('aa9145bdca164b779256441b25bf4623', 'd647ba631e6c439bbfb968ff84b1aac5', 'Minimax', 'image-01-live', 'text2image', 'https://api.minimaxi.com/v1/', now(), now(), NULL),
  ('56416c7a62a8415caf917510ac6123b3', 'e93bdc713dd14f16a9a6a9b282ad7d1d', 'OpenAI', 'gpt-4.1', 'VLM', 'https://api.openai.com/v1/', now(), now(), NULL),
  ('32ec4e55a02e4e798b55182fde03d659', 'e93bdc713dd14f16a9a6a9b282ad7d1d', 'OpenAI', 'gpt-4.1-mini', 'VLM', 'https://api.openai.com/v1/', now(), now(), NULL),
  ('8c7daa67c2be45588503644c15dbb431', 'e93bdc713dd14f16a9a6a9b282ad7d1d', 'OpenAI', 'gpt-4o-mini', 'VLM', 'https://api.openai.com/v1/', now(), now(), NULL),
  ('36a0b08971c64f7e96d301bff29c5480', 'e93bdc713dd14f16a9a6a9b282ad7d1d', 'OpenAI', 'gpt-4o-mini-transcribe', 'stt', 'https://api.openai.com/v1/', now(), now(), NULL),
  ('4693f50a5eaf493fbfbe18edce5456b8', 'e93bdc713dd14f16a9a6a9b282ad7d1d', 'OpenAI', 'gpt-4o-transcribe', 'stt', 'https://api.openai.com/v1/', now(), now(), NULL),
  ('09782e12b2d74a66a54f17960f43bc51', 'e93bdc713dd14f16a9a6a9b282ad7d1d', 'OpenAI', 'gpt-5', 'VLM', 'https://api.openai.com/v1/', now(), now(), NULL),
  ('9a4a9f870fbb4a13b6479b9fa5f126ea', 'e93bdc713dd14f16a9a6a9b282ad7d1d', 'OpenAI', 'gpt-5-mini', 'VLM', 'https://api.openai.com/v1/', now(), now(), NULL),
  ('0c380e98b320438c89ee208b7e1a45dc', 'e93bdc713dd14f16a9a6a9b282ad7d1d', 'OpenAI', 'gpt-5-nano', 'VLM', 'https://api.openai.com/v1/', now(), now(), NULL),
  ('e9af69b1d86d46828c5d4e7aa855f0a6', 'e93bdc713dd14f16a9a6a9b282ad7d1d', 'OpenAI', 'gpt-5-pro', 'VLM', 'https://api.openai.com/v1/', now(), now(), NULL),
  ('3d03ea834c604160b102e41ffca607b8', 'e93bdc713dd14f16a9a6a9b282ad7d1d', 'OpenAI', 'gpt-5.1', 'VLM', 'https://api.openai.com/v1/', now(), now(), NULL),
  ('125a3e50b966460c99395bcb9c707f6c', 'e93bdc713dd14f16a9a6a9b282ad7d1d', 'OpenAI', 'gpt-5.2', 'VLM', 'https://api.openai.com/v1/', now(), now(), NULL),
  ('2d15464ca08344048c171568c60c5944', 'e93bdc713dd14f16a9a6a9b282ad7d1d', 'OpenAI', 'gpt-5.2-pro', 'VLM', 'https://api.openai.com/v1/', now(), now(), NULL),
  ('683f37babe444ff39fece9d85a3c77b6', 'e93bdc713dd14f16a9a6a9b282ad7d1d', 'OpenAI', 'gpt-5.4', 'VLM', 'https://api.openai.com/v1/', now(), now(), NULL),
  ('55d33b15511b4966bcf8fe45496f3b26', 'e93bdc713dd14f16a9a6a9b282ad7d1d', 'OpenAI', 'gpt-5.4-mini', 'VLM', 'https://api.openai.com/v1/', now(), now(), NULL),
  ('1a417e77395c4dd1926e1fa0d8f1d84c', 'e93bdc713dd14f16a9a6a9b282ad7d1d', 'OpenAI', 'gpt-5.4-nano', 'VLM', 'https://api.openai.com/v1/', now(), now(), NULL),
  ('1f6dfa974aca45b5915aa56c020b9a87', 'e93bdc713dd14f16a9a6a9b282ad7d1d', 'OpenAI', 'gpt-5.4-pro', 'VLM', 'https://api.openai.com/v1/', now(), now(), NULL),
  ('fd74c337ac8a4518a1bcedccfa3ddfe4', 'e93bdc713dd14f16a9a6a9b282ad7d1d', 'OpenAI', 'gpt-5.5', 'VLM', 'https://api.openai.com/v1/', now(), now(), NULL),
  ('45c97575865840b0a31ebd1425617169', 'e93bdc713dd14f16a9a6a9b282ad7d1d', 'OpenAI', 'gpt-5.5-pro', 'VLM', 'https://api.openai.com/v1/', now(), now(), NULL),
  ('b7766f7c03a240a7a6eb5d94dc9d74d9', 'e93bdc713dd14f16a9a6a9b282ad7d1d', 'OpenAI', 'gpt-image-1.5', 'text2image', 'https://api.openai.com/v1/', now(), now(), NULL),
  ('e40c246fd02340ea94b577f2f8a2e9b3', 'e93bdc713dd14f16a9a6a9b282ad7d1d', 'OpenAI', 'gpt-image-2', 'text2image', 'https://api.openai.com/v1/', now(), now(), NULL),
  ('1f7b90daab694e80961c7f5d6ae927b6', 'e93bdc713dd14f16a9a6a9b282ad7d1d', 'OpenAI', 'o3', 'VLM', 'https://api.openai.com/v1/', now(), now(), NULL),
  ('f28d2f0c69cd4311a426586e1692f71d', 'e93bdc713dd14f16a9a6a9b282ad7d1d', 'OpenAI', 'rerank-multilingual-v3.0', 'rerank', 'https://api.openai.com/v1/', now(), now(), NULL),
  ('cd3f7ea440454ceb94a502684ef6ccdd', 'e93bdc713dd14f16a9a6a9b282ad7d1d', 'OpenAI', 'text-embedding-3-large', 'embedding', 'https://api.openai.com/v1/', now(), now(), NULL),
  ('266bce11c6d34b0c8c2a3b96dc2cc93e', 'e93bdc713dd14f16a9a6a9b282ad7d1d', 'OpenAI', 'text-embedding-3-small', 'embedding', 'https://api.openai.com/v1/', now(), now(), NULL),
  ('43d221d2b9134bad98c82895d851034d', 'e93bdc713dd14f16a9a6a9b282ad7d1d', 'OpenAI', 'text-embedding-ada-002', 'embedding', 'https://api.openai.com/v1/', now(), now(), NULL),
  ('b0d0bd95b78e4db8bfc95a072f20bc9b', 'e93bdc713dd14f16a9a6a9b282ad7d1d', 'OpenAI', 'tts-1', 'tts', 'https://api.openai.com/v1/', now(), now(), NULL),
  ('1d00fc62a1ad45c98e3b778400d9f0c0', 'e93bdc713dd14f16a9a6a9b282ad7d1d', 'OpenAI', 'tts-1-hd', 'tts', 'https://api.openai.com/v1/', now(), now(), NULL),
  ('cc653dca9b83445f99d6410cc290f4f8', 'e93bdc713dd14f16a9a6a9b282ad7d1d', 'OpenAI', 'whisper-1', 'stt', 'https://api.openai.com/v1/', now(), now(), NULL),
  ('9ba563a99ae9428c8eef6d2e3843ffbd', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'Fun-ASR', 'stt', 'https://dashscope.aliyuncs.com/api/v1', now(), now(), NULL),
  ('9f9c119788094005a28c6cb45d3a1b65', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'Gummy', 'stt', 'https://dashscope.aliyuncs.com/api/v1', now(), now(), NULL),
  ('a019c43df9ab4c1daa78da165e1d420e', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'SenseVoice', 'stt', 'https://dashscope.aliyuncs.com/api/v1', now(), now(), NULL),
  ('2bd4d4c1fa874b58885c7ba7decec0db', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'cosyvoice-v1', 'tts', 'https://dashscope.aliyuncs.com/api/v1', now(), now(), NULL),
  ('a7dd7e40e1a84b19bd822de534cc6d95', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'cosyvoice-v2', 'tts', 'https://dashscope.aliyuncs.com/api/v1', now(), now(), NULL),
  ('6f6d7cc031ab42e8a65b80885fcbf7ce', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'gte-rerank-v2', 'rerank', 'https://dashscope.aliyuncs.com/', now(), now(), NULL),
  ('9b458247e1c04daba1ff4bb402ef2753', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'multimodal-embedding-v1', 'multimodal_embedding', 'https://dashscope.aliyuncs.com/', now(), now(), NULL),
  ('b18b7d514c224b3ab7646d41d574d33b', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'paraformer-v2', 'stt', 'https://dashscope.aliyuncs.com/api/v1', now(), now(), NULL),
  ('6a12b0eb3613413fbcc9053344ce0541', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'qvq-72b-preview', 'VLM', 'https://dashscope.aliyuncs.com/', now(), now(), NULL),
  ('691a89881f5a41be94cb85d3ba623643', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'qvq-max', 'VLM', 'https://dashscope.aliyuncs.com/', now(), now(), NULL),
  ('1a6051a463c94439bc6b2cb6bed1fb70', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'qvq-plus', 'VLM', 'https://dashscope.aliyuncs.com/', now(), now(), NULL),
  ('1b9216f3b2184137aa1b804453d46bb4', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'qwen-audio-turbo', 'stt', 'https://dashscope.aliyuncs.com/api/v1', now(), now(), NULL),
  ('6c0c59028c594b7d921ae737ee7a3b35', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'qwen-flash', 'llm', 'https://dashscope.aliyuncs.com/', now(), now(), NULL),
  ('ce1d8a1975c04142b2b0087dbd79c4eb', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'qwen-flash-2025-07-28', 'llm', 'https://dashscope.aliyuncs.com/', now(), now(), NULL),
  ('5351b6a1dd6943598b87312d0f90232c', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'qwen-long', 'llm', 'https://dashscope.aliyuncs.com/', now(), now(), NULL),
  ('4dc66a8e423f498db68a128d7b21df0c', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'qwen-max', 'llm', 'https://dashscope.aliyuncs.com/', now(), now(), NULL),
  ('196083eb497b470cb8b967b90e387555', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'qwen-max-2025-01-25', 'llm', 'https://dashscope.aliyuncs.com/', now(), now(), NULL),
  ('f61f60eaa99845d1bbc4c1cdd4d90457', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'qwen-plus', 'llm', 'https://dashscope.aliyuncs.com/', now(), now(), NULL),
  ('8c12aaa3305044fda6f43b1477518ee2', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'qwen-plus-2025-12-01', 'llm', 'https://dashscope.aliyuncs.com/', now(), now(), NULL),
  ('32c8552ce8fc4b24ab87abe338b477b6', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'qwen-tts', 'tts', 'https://dashscope.aliyuncs.com/api/v1', now(), now(), NULL),
  ('57b9a917f2204d4aa0eeefb9061830ac', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'qwen-turbo', 'llm', 'https://dashscope.aliyuncs.com/', now(), now(), NULL),
  ('aa00a0518a9d4bf9afdb5f3cce32c912', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'qwen-vl-max', 'VLM', 'https://dashscope.aliyuncs.com/', now(), now(), NULL),
  ('a143b902321540c18aa95593c5861edf', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'qwen-vl-ocr', 'VLM', 'https://dashscope.aliyuncs.com/', now(), now(), NULL),
  ('f21fb98b9758451a839c9f5ad9d62d0a', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'qwen-vl-plus', 'VLM', 'https://dashscope.aliyuncs.com/', now(), now(), NULL),
  ('9c69e35af3554040ad2e04986e2ddec8', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'qwen2.5-0.5b-instruct', 'llm', 'https://dashscope.aliyuncs.com/', now(), now(), NULL),
  ('accbc1ca794d46a4a9a5ee1e53475dd5', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'qwen2.5-1.5b-instruct', 'llm', 'https://dashscope.aliyuncs.com/', now(), now(), NULL),
  ('675fbc711dae4b7f80f69beae22fedea', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'qwen2.5-14b-instruct', 'llm', 'https://dashscope.aliyuncs.com/', now(), now(), NULL),
  ('416c8586897c4f72b071b65ebe2529ca', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'qwen2.5-14b-instruct-1m', 'llm', 'https://dashscope.aliyuncs.com/', now(), now(), NULL),
  ('ef3bd1ccbd364e4a8ae430579b535765', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'qwen2.5-32b-instruct', 'llm', 'https://dashscope.aliyuncs.com/', now(), now(), NULL),
  ('a7c80b1b544a4ab1beec1379374940c4', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'qwen2.5-3b-instruct', 'llm', 'https://dashscope.aliyuncs.com/', now(), now(), NULL),
  ('022d899f2c8841f1a055d35ec9aa6d62', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'qwen2.5-72b-instruct', 'llm', 'https://dashscope.aliyuncs.com/', now(), now(), NULL),
  ('b71a087327e7435fa7bb1a8aed5287e0', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'qwen2.5-7b-instruct', 'llm', 'https://dashscope.aliyuncs.com/', now(), now(), NULL),
  ('93fc24cfebb746859c2c169994c41ef5', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'qwen2.5-7b-instruct-1m', 'llm', 'https://dashscope.aliyuncs.com/', now(), now(), NULL),
  ('4489bd3722b64a1e92fa0b41e53c6ab4', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'qwen2.5-vl-embedding', 'multimodal_embedding', 'https://dashscope.aliyuncs.com/', now(), now(), NULL),
  ('ae1e7fe518014f00a824ff619a111612', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'qwen3-0.6b', 'llm', 'https://dashscope.aliyuncs.com/', now(), now(), NULL),
  ('2b6d890cdb99472d93ae0c679c092960', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'qwen3-1.7b', 'llm', 'https://dashscope.aliyuncs.com/', now(), now(), NULL),
  ('cb566f0db4b44746a2caf721d8002e0e', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'qwen3-14b', 'llm', 'https://dashscope.aliyuncs.com/', now(), now(), NULL),
  ('5fdd97cbd8554037b2c93e428b47f904', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'qwen3-235b-a22b', 'llm', 'https://dashscope.aliyuncs.com/', now(), now(), NULL),
  ('c0bbcd2e31794bf289b7906cb427c0ac', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'qwen3-235b-a22b-instruct-2507', 'llm', 'https://dashscope.aliyuncs.com/', now(), now(), NULL),
  ('51d78498aa1543eaa3dfabe34d0f5bcc', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'qwen3-235b-a22b-thinking-2507', 'llm', 'https://dashscope.aliyuncs.com/', now(), now(), NULL),
  ('b32b1f1ce31a4526ba78dc687ed372e4', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'qwen3-30b-a3b', 'llm', 'https://dashscope.aliyuncs.com/', now(), now(), NULL),
  ('66e4d916d8d94614800d21f31e9f476c', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'qwen3-30b-a3b-instruct-2507', 'llm', 'https://dashscope.aliyuncs.com/', now(), now(), NULL),
  ('cb54e2fbc500410d835280802385a2df', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'qwen3-30b-a3b-thinking-2507', 'llm', 'https://dashscope.aliyuncs.com/', now(), now(), NULL),
  ('41b18563b7a54becabcf106c106f987d', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'qwen3-32b', 'llm', 'https://dashscope.aliyuncs.com/', now(), now(), NULL),
  ('f479fadae80f4ec6929606f5ef542a9f', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'qwen3-4b', 'llm', 'https://dashscope.aliyuncs.com/', now(), now(), NULL),
  ('0ae4658ce66c451a8ab9441649e95746', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'qwen3-8b', 'llm', 'https://dashscope.aliyuncs.com/', now(), now(), NULL),
  ('b1ebdce5447e49789f9226ae3e70d4b0', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'qwen3-asr-flash', 'stt', 'https://dashscope.aliyuncs.com/api/v1', now(), now(), NULL),
  ('c7fd2d7ede1b4059b1fa222594421361', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'qwen3-embedding-32m', 'embedding', 'https://dashscope.aliyuncs.com/', now(), now(), NULL),
  ('e4c28de1a0e74537b0d55622b79c8b84', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'qwen3-next-80b-a3b-instruct', 'llm', 'https://dashscope.aliyuncs.com/', now(), now(), NULL),
  ('9a41582a78284e30b29bc15019b745cc', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'qwen3-next-80b-a3b-thinking', 'llm', 'https://dashscope.aliyuncs.com/', now(), now(), NULL),
  ('823fc6e3bd0e4d4ba590d5a80caf37e6', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'qwen3-rerank', 'rerank', 'https://dashscope.aliyuncs.com/', now(), now(), NULL),
  ('3aad40783023494184b6786169e7b577', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'qwen3-vl-embedding', 'multimodal_embedding', 'https://dashscope.aliyuncs.com/', now(), now(), NULL),
  ('ce8a5f445a8c40a592df0dbf92208cc6', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'qwen3-vl-flash', 'VLM', 'https://dashscope.aliyuncs.com/', now(), now(), NULL),
  ('1855b86658974444a961a3be00a7d1cc', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'qwen3-vl-flash-us', 'VLM', 'https://dashscope.aliyuncs.com/', now(), now(), NULL),
  ('ab75742661e34f35b898f29be25ed9ea', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'qwen3-vl-plus', 'VLM', 'https://dashscope.aliyuncs.com/', now(), now(), NULL),
  ('502aa1b0c38443cc86641bf0bdb6ae5d', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'qwen3-vl-rerank', 'rerank', 'https://dashscope.aliyuncs.com/', now(), now(), NULL),
  ('9cbd07970a234594a6e480698395156a', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'qwen3.5-122b-a10b', 'VLM', 'https://dashscope.aliyuncs.com/', now(), now(), NULL),
  ('175aa913e2054caf841c9d4b6f56e98f', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'qwen3.5-27b', 'VLM', 'https://dashscope.aliyuncs.com/', now(), now(), NULL),
  ('76fde238b39c4fd7accaf21320205098', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'qwen3.5-35b-a3b', 'VLM', 'https://dashscope.aliyuncs.com/', now(), now(), NULL),
  ('50547e2cbc944ee4addba31d758c748b', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'qwen3.5-397b-a17b', 'VLM', 'https://dashscope.aliyuncs.com/', now(), now(), NULL),
  ('c858490b3d91437890aac01adeff6b81', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'qwen3.5-4b', 'VLM', 'https://dashscope.aliyuncs.com/', now(), now(), NULL),
  ('8327f658d7b045f4af62174ec54bceee', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'qwen3.5-9b', 'VLM', 'https://dashscope.aliyuncs.com/', now(), now(), NULL),
  ('edf825f111d74144b793afba73da91e4', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'qwen3.5-flash', 'VLM', 'https://dashscope.aliyuncs.com/', now(), now(), NULL),
  ('1078eefb9c3c46719dda1feda2c245fd', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'qwen3.5-plus', 'VLM', 'https://dashscope.aliyuncs.com/', now(), now(), NULL),
  ('9144a6726daa47a6b1e0d6643ee2ea36', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'qwen3.6-27b', 'VLM', 'https://dashscope.aliyuncs.com/', now(), now(), NULL),
  ('76d5b34da4b943228ac89eaae3567ffa', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'qwen3.6-35b-a3b', 'VLM', 'https://dashscope.aliyuncs.com/', now(), now(), NULL),
  ('7e62238041454dd7948d13fcece89b39', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'qwen3.6-flash', 'VLM', 'https://dashscope.aliyuncs.com/', now(), now(), NULL),
  ('e2ad22e2172f4f0b8c0bdd48e8dc84d8', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'qwen3.6-max-preview', 'VLM', 'https://dashscope.aliyuncs.com/', now(), now(), NULL),
  ('5d065105f4824904aef1fee2d165e4de', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'qwen3.6-plus', 'VLM', 'https://dashscope.aliyuncs.com/', now(), now(), NULL),
  ('c65c97668bad4f958a85bcd855743b55', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'qwq-32b', 'VLM', 'https://dashscope.aliyuncs.com/', now(), now(), NULL),
  ('3391c1f5cdfc4048b7c88931f03995e7', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'qwq-32b-preview', 'VLM', 'https://dashscope.aliyuncs.com/', now(), now(), NULL),
  ('15ebc504625347eb9989aa2f6849d297', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'qwq-plus', 'VLM', 'https://dashscope.aliyuncs.com/', now(), now(), NULL),
  ('22fd89b044b841f5b95ae0843a5284cd', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'sambert', 'tts', 'https://dashscope.aliyuncs.com/api/v1', now(), now(), NULL),
  ('7753133dce0c4ae4b64fc8f0c82a3139', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'sensevoice-small', 'stt', 'https://dashscope.aliyuncs.com/api/v1', now(), now(), NULL),
  ('6e17433342ef48eda83b5a5d2f01329d', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'text-embedding-async-v1', 'embedding', 'https://dashscope.aliyuncs.com/', now(), now(), NULL),
  ('081b7781b76340e4bc587b11d05a75d0', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'text-embedding-async-v2', 'embedding', 'https://dashscope.aliyuncs.com/', now(), now(), NULL),
  ('9d44ac102d204e288bc3b4cbcc4ce115', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'text-embedding-v1', 'embedding', 'https://dashscope.aliyuncs.com/', now(), now(), NULL),
  ('8c7f31e34d0c4cb9a73d81eec8b72345', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'text-embedding-v2', 'embedding', 'https://dashscope.aliyuncs.com/', now(), now(), NULL),
  ('f7dffff1814541cfb504895c89b82c7a', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'text-embedding-v3', 'embedding', 'https://dashscope.aliyuncs.com/', now(), now(), NULL),
  ('6f23248891544441947d402d574a0830', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'text-embedding-v4', 'embedding', 'https://dashscope.aliyuncs.com/', now(), now(), NULL),
  ('01651066a3914a01b299383cfb8ba76f', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'tongyi-embedding-vision-flash', 'multimodal_embedding', 'https://dashscope.aliyuncs.com/', now(), now(), NULL),
  ('c02d28c19e7c45ae82e8cef49367dc0e', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'tongyi-embedding-vision-flash-2026-03-06', 'multimodal_embedding', 'https://dashscope.aliyuncs.com/', now(), now(), NULL),
  ('ec7858cf9cb44bcd8622574fd0113046', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'tongyi-embedding-vision-plus', 'multimodal_embedding', 'https://dashscope.aliyuncs.com/', now(), now(), NULL),
  ('b0453c19645e4987a47c93c495a1052d', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'tongyi-embedding-vision-plus-2026-03-06', 'multimodal_embedding', 'https://dashscope.aliyuncs.com/', now(), now(), NULL),
  ('519a43a62c3f4e7fb34a0eaa2ce7abda', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'wan2.6-t2i', 'text2image', 'https://dashscope.aliyuncs.com/', now(), now(), NULL),
  ('33bdc8d9a4de49038a9de9ae30d8f263', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'wanx2.1-t2i-pro', 'text2image', 'https://dashscope.aliyuncs.com/', now(), now(), NULL),
  ('022626323f7347daa07dc5ef1457e8e1', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'wanx2.1-t2i-turbo', 'text2image', 'https://dashscope.aliyuncs.com/', now(), now(), NULL),
  ('54a26c0b019f480cad1b97bda6633e68', '72ebcc11418d432887c28145b1600722', 'SenseNova', 'DeepSeek V4 Flash', 'llm', 'https://api.sensenova.cn/compatible-mode/v1/', now(), now(), NULL),
  ('348dc1374d804bc4aec9fbded2ec5cd2', '72ebcc11418d432887c28145b1600722', 'SenseNova', 'DeepSeek-R1', 'llm', 'https://api.sensenova.cn/compatible-mode/v1/', now(), now(), NULL),
  ('db0cbef8672949ccaf153891c089ec20', '72ebcc11418d432887c28145b1600722', 'SenseNova', 'DeepSeek-R1-Distill-Qwen-14B', 'llm', 'https://api.sensenova.cn/compatible-mode/v1/', now(), now(), NULL),
  ('e847fd47a3ed4f0a9c5680b05a74966c', '72ebcc11418d432887c28145b1600722', 'SenseNova', 'DeepSeek-V3', 'llm', 'https://api.sensenova.cn/compatible-mode/v1/', now(), now(), NULL),
  ('b70e180e1dc543db8e685a01b4fb6e53', '72ebcc11418d432887c28145b1600722', 'SenseNova', 'DeepSeek-V3-1', 'llm', 'https://api.sensenova.cn/compatible-mode/v1/', now(), now(), NULL),
  ('929abfea334d415fb64cadf52ae4216d', '72ebcc11418d432887c28145b1600722', 'SenseNova', 'Qwen2-5-Coder', 'llm', 'https://api.sensenova.cn/compatible-mode/v1/', now(), now(), NULL),
  ('2fee9df85afa497298fc04ee6c9663a6', '72ebcc11418d432887c28145b1600722', 'SenseNova', 'Qwen3-235B', 'llm', 'https://api.sensenova.cn/compatible-mode/v1/', now(), now(), NULL),
  ('85aa77c21c1e4ccb986c1a9509b33de3', '72ebcc11418d432887c28145b1600722', 'SenseNova', 'Qwen3-32B', 'llm', 'https://api.sensenova.cn/compatible-mode/v1/', now(), now(), NULL),
  ('85a94c85b05b45dea0d0c3994fd8a0b6', '72ebcc11418d432887c28145b1600722', 'SenseNova', 'Qwen3-Coder', 'llm', 'https://api.sensenova.cn/compatible-mode/v1/', now(), now(), NULL),
  ('fd224fa531a242d0ae196b0568e1dc8c', '72ebcc11418d432887c28145b1600722', 'SenseNova', 'SenseChat', 'llm', 'https://api.sensenova.cn/compatible-mode/v1/', now(), now(), NULL),
  ('1620fb658a6548fc9e1025689d6a386f', '72ebcc11418d432887c28145b1600722', 'SenseNova', 'SenseChat-128K', 'llm', 'https://api.sensenova.cn/compatible-mode/v1/', now(), now(), NULL),
  ('4acf78b5d5ba464ab6aeb1725ce705cc', '72ebcc11418d432887c28145b1600722', 'SenseNova', 'SenseChat-5', 'llm', 'https://api.sensenova.cn/compatible-mode/v1/', now(), now(), NULL),
  ('0c90aca5bda2439bb90460bb7c92dc5e', '72ebcc11418d432887c28145b1600722', 'SenseNova', 'SenseChat-5-1202', 'llm', 'https://api.sensenova.cn/compatible-mode/v1/', now(), now(), NULL),
  ('a12e3f21d9f74c7689ed3b8c884b1d49', '72ebcc11418d432887c28145b1600722', 'SenseNova', 'SenseChat-5-Cantonese', 'llm', 'https://api.sensenova.cn/compatible-mode/v1/', now(), now(), NULL),
  ('fe06cb8df22342febeb1bf2577311fd4', '72ebcc11418d432887c28145b1600722', 'SenseNova', 'SenseChat-Turbo', 'llm', 'https://api.sensenova.cn/compatible-mode/v1/', now(), now(), NULL),
  ('b1cc07bb2a034359a227d465248c846f', '72ebcc11418d432887c28145b1600722', 'SenseNova', 'SenseChat-Turbo-1202', 'llm', 'https://api.sensenova.cn/compatible-mode/v1/', now(), now(), NULL),
  ('8d4ad6291914439abc8a7ddf47ae8ee9', '72ebcc11418d432887c28145b1600722', 'SenseNova', 'SenseChat-Vision', 'VLM', 'https://api.sensenova.cn/compatible-mode/v1/', now(), now(), NULL),
  ('7a35abff8586484690bf348794c938b8', '72ebcc11418d432887c28145b1600722', 'SenseNova', 'SenseNova 6.7 Flash-Lite', 'VLM', 'https://api.sensenova.cn/compatible-mode/v1/', now(), now(), NULL),
  ('80da86ebb0d04a4e9fb327a51e243fad', '72ebcc11418d432887c28145b1600722', 'SenseNova', 'SenseNova U1 Fast', 'VLM', 'https://api.sensenova.cn/compatible-mode/v1/', now(), now(), NULL),
  ('0f571f3b9142412aade87659eafe5124', '72ebcc11418d432887c28145b1600722', 'SenseNova', 'SenseNova-V6-5-Pro', 'VLM', 'https://api.sensenova.cn/compatible-mode/v1/', now(), now(), NULL),
  ('0da6b0e7d9454a3298bd2ec3bf7dd6a6', '72ebcc11418d432887c28145b1600722', 'SenseNova', 'SenseNova-V6-5-Turbo', 'VLM', 'https://api.sensenova.cn/compatible-mode/v1/', now(), now(), NULL),
  ('fbbd9949ffb5478189501c73ba165b4d', '72ebcc11418d432887c28145b1600722', 'SenseNova', 'SenseNova-V6-Pro', 'VLM', 'https://api.sensenova.cn/compatible-mode/v1/', now(), now(), NULL),
  ('970a1ffb08fe4225a4431a596792ff8b', '72ebcc11418d432887c28145b1600722', 'SenseNova', 'SenseNova-V6-Reasoner', 'VLM', 'https://api.sensenova.cn/compatible-mode/v1/', now(), now(), NULL),
  ('b679b6bca1ee46f5b5785d4e1eddcb94', '72ebcc11418d432887c28145b1600722', 'SenseNova', 'SenseNova-V6-Turbo', 'VLM', 'https://api.sensenova.cn/compatible-mode/v1/', now(), now(), NULL),
  ('a9d0362d7f0140cc89bc3fd650ce6d09', '72ebcc11418d432887c28145b1600722', 'SenseNova', 'nova-embedding-stable', 'embedding', 'https://api.sensenova.cn/v1/llm/embeddings', now(), now(), NULL),
  ('e2b2188131684dd391cec9113b67bac9', 'f756580d5758487ea88691268169308c', 'SiliconFlow', 'deepseek-ai/DeepSeek-V4-Flash', 'llm', 'https://api.siliconflow.cn/v1/', now(), now(), NULL),
  ('71ddcc920c71437caa3920d3b70985d7', 'f756580d5758487ea88691268169308c', 'SiliconFlow', 'Pro/moonshotai/Kimi-K2.6', 'VLM', 'https://api.siliconflow.cn/v1/', now(), now(), NULL),
  ('c731207494d7414fb31667a7ec243ad3', 'f756580d5758487ea88691268169308c', 'SiliconFlow', 'Pro/zai-org/GLM-5.1', 'llm', 'https://api.siliconflow.cn/v1/', now(), now(), NULL),
  ('b3ee51b2aba74f7ebc8fd1937a62cb1f', 'f756580d5758487ea88691268169308c', 'SiliconFlow', 'MiniMaxAI/MiniMax-M2.5', 'llm', 'https://api.siliconflow.cn/v1/', now(), now(), NULL),
  ('991d7206e821408c8c48704f0bd1e3d7', 'f756580d5758487ea88691268169308c', 'SiliconFlow', 'Pro/MiniMaxAI/MiniMax-M2.5', 'llm', 'https://api.siliconflow.cn/v1/', now(), now(), NULL),
  ('caea318b97e641b4b5c76435819024f5', 'f756580d5758487ea88691268169308c', 'SiliconFlow', 'Pro/zai-org/GLM-5', 'llm', 'https://api.siliconflow.cn/v1/', now(), now(), NULL),
  ('95ad74de6d6441439c4ac4e2055f5e7e', 'f756580d5758487ea88691268169308c', 'SiliconFlow', 'Pro/deepseek-ai/DeepSeek-V3.2', 'llm', 'https://api.siliconflow.cn/v1/', now(), now(), NULL),
  ('fc6c5dd43d464be9b2a1d6435f589a2c', 'f756580d5758487ea88691268169308c', 'SiliconFlow', 'Pro/moonshotai/Kimi-K2.5', 'VLM', 'https://api.siliconflow.cn/v1/', now(), now(), NULL),
  ('8802b2d9b55e4f02848c5075b95802ea', 'f756580d5758487ea88691268169308c', 'SiliconFlow', 'Pro/zai-org/GLM-4.7', 'llm', 'https://api.siliconflow.cn/v1/', now(), now(), NULL),
  ('6a2678b3a04c46169bb8926d340875d8', 'f756580d5758487ea88691268169308c', 'SiliconFlow', 'deepseek-ai/DeepSeek-V3.2', 'llm', 'https://api.siliconflow.cn/v1/', now(), now(), NULL),
  ('30f4b82a45bd42b7ac5a5a29ae40e204', 'f756580d5758487ea88691268169308c', 'SiliconFlow', 'Pro/deepseek-ai/DeepSeek-V3.1-Terminus', 'llm', 'https://api.siliconflow.cn/v1/', now(), now(), NULL),
  ('2f5d2112afdd4d82bde3035ac36a7bcb', 'f756580d5758487ea88691268169308c', 'SiliconFlow', 'deepseek-ai/DeepSeek-V3.1-Terminus', 'llm', 'https://api.siliconflow.cn/v1/', now(), now(), NULL),
  ('c60403bf002c42ecbbf819e2fb607436', 'f756580d5758487ea88691268169308c', 'SiliconFlow', 'Qwen/Qwen3.6-35B-A3B', 'VLM', 'https://api.siliconflow.cn/v1/', now(), now(), NULL),
  ('883fd871156f47b49ea24433e2fdc670', 'f756580d5758487ea88691268169308c', 'SiliconFlow', 'Qwen/Qwen3.6-27B', 'VLM', 'https://api.siliconflow.cn/v1/', now(), now(), NULL),
  ('f26a63d0da8248879f41c428286ae604', 'f756580d5758487ea88691268169308c', 'SiliconFlow', 'Qwen/Qwen3.5-397B-A17B', 'VLM', 'https://api.siliconflow.cn/v1/', now(), now(), NULL),
  ('9053d82e3f5449c9910499e82da05ecd', 'f756580d5758487ea88691268169308c', 'SiliconFlow', 'Qwen/Qwen3.5-122B-A10B', 'VLM', 'https://api.siliconflow.cn/v1/', now(), now(), NULL),
  ('d6de46f404c04b6d891c0f5a50f0c2ea', 'f756580d5758487ea88691268169308c', 'SiliconFlow', 'Qwen/Qwen3.5-35B-A3B', 'VLM', 'https://api.siliconflow.cn/v1/', now(), now(), NULL),
  ('687772c99f834f4181934fc95937f0a4', 'f756580d5758487ea88691268169308c', 'SiliconFlow', 'Qwen/Qwen3.5-27B', 'VLM', 'https://api.siliconflow.cn/v1/', now(), now(), NULL),
  ('ce3bed75597f4797878b246e86464f0e', 'f756580d5758487ea88691268169308c', 'SiliconFlow', 'Qwen/Qwen3.5-9B', 'VLM', 'https://api.siliconflow.cn/v1/', now(), now(), NULL),
  ('18563bd76c204227a49196760ee70db3', 'f756580d5758487ea88691268169308c', 'SiliconFlow', 'Qwen/Qwen3.5-4B', 'VLM', 'https://api.siliconflow.cn/v1/', now(), now(), NULL),
  ('eac3833e699241cb83d0088095b65729', 'f756580d5758487ea88691268169308c', 'SiliconFlow', 'deepseek-ai/DeepSeek-R1', 'llm', 'https://api.siliconflow.cn/v1/', now(), now(), NULL),
  ('49ab399ce2ed4f468efac2b0ea9b5649', 'f756580d5758487ea88691268169308c', 'SiliconFlow', 'Pro/deepseek-ai/DeepSeek-R1', 'llm', 'https://api.siliconflow.cn/v1/', now(), now(), NULL),
  ('406f17e5510d4919856957afcd991256', 'f756580d5758487ea88691268169308c', 'SiliconFlow', 'deepseek-ai/DeepSeek-V3', 'llm', 'https://api.siliconflow.cn/v1/', now(), now(), NULL),
  ('eaa6b87d07ce4f4b977a78a60fe9525d', 'f756580d5758487ea88691268169308c', 'SiliconFlow', 'Pro/deepseek-ai/DeepSeek-V3', 'llm', 'https://api.siliconflow.cn/v1/', now(), now(), NULL),
  ('34c04be1d0c4463b90dcad622c6645c5', 'f756580d5758487ea88691268169308c', 'SiliconFlow', 'stepfun-ai/Step-3.5-Flash', 'llm', 'https://api.siliconflow.cn/v1/', now(), now(), NULL),
  ('26ea7d4b56f344f4bf24ee1754b998f7', 'f756580d5758487ea88691268169308c', 'SiliconFlow', 'zai-org/GLM-4.6V', 'VLM', 'https://api.siliconflow.cn/v1/', now(), now(), NULL),
  ('03be9f544e724472ba7c4a93455b3923', 'f756580d5758487ea88691268169308c', 'SiliconFlow', 'moonshotai/Kimi-K2-Thinking', 'llm', 'https://api.siliconflow.cn/v1/', now(), now(), NULL),
  ('5a061aca2d3d4891ba28514ca62fb07e', 'f756580d5758487ea88691268169308c', 'SiliconFlow', 'Pro/moonshotai/Kimi-K2-Thinking', 'llm', 'https://api.siliconflow.cn/v1/', now(), now(), NULL),
  ('dcbe2afc9f8d4cf281b47f32b96e8930', 'f756580d5758487ea88691268169308c', 'SiliconFlow', 'zai-org/GLM-4.6', 'llm', 'https://api.siliconflow.cn/v1/', now(), now(), NULL),
  ('d7aa1d4f5bdf4a61b89077c1769f6195', 'f756580d5758487ea88691268169308c', 'SiliconFlow', 'Qwen/Qwen3-VL-32B-Instruct', 'VLM', 'https://api.siliconflow.cn/v1/', now(), now(), NULL),
  ('bdf9f349c1314515b42402b0b08c1c7b', 'f756580d5758487ea88691268169308c', 'SiliconFlow', 'Qwen/Qwen3-VL-32B-Thinking', 'VLM', 'https://api.siliconflow.cn/v1/', now(), now(), NULL),
  ('1d21f23a067044e4b75c088ce4220041', 'f756580d5758487ea88691268169308c', 'SiliconFlow', 'Qwen/Qwen3-VL-8B-Instruct', 'VLM', 'https://api.siliconflow.cn/v1/', now(), now(), NULL),
  ('78daf713d00c47c48ef6c170ccf3bc83', 'f756580d5758487ea88691268169308c', 'SiliconFlow', 'Qwen/Qwen3-VL-8B-Thinking', 'VLM', 'https://api.siliconflow.cn/v1/', now(), now(), NULL),
  ('0a80d6ce6ccf46c394abd2e9931af99b', 'f756580d5758487ea88691268169308c', 'SiliconFlow', 'Qwen/Qwen3-VL-30B-A3B-Instruct', 'VLM', 'https://api.siliconflow.cn/v1/', now(), now(), NULL),
  ('c3b38ebbcfe642589e1eb7e2c077f154', 'f756580d5758487ea88691268169308c', 'SiliconFlow', 'Qwen/Qwen3-VL-30B-A3B-Thinking', 'VLM', 'https://api.siliconflow.cn/v1/', now(), now(), NULL),
  ('aeecc5036cc04c50bc9c82f854c87f5b', 'f756580d5758487ea88691268169308c', 'SiliconFlow', 'moonshotai/Kimi-K2-Instruct-0905', 'llm', 'https://api.siliconflow.cn/v1/', now(), now(), NULL),
  ('d085a0af2d4a48199e7540a174899ff8', 'f756580d5758487ea88691268169308c', 'SiliconFlow', 'Pro/moonshotai/Kimi-K2-Instruct-0905', 'llm', 'https://api.siliconflow.cn/v1/', now(), now(), NULL),
  ('e9e6abaf0e2c479483af93c41c6b4aa4', 'f756580d5758487ea88691268169308c', 'SiliconFlow', 'inclusionAI/Ring-flash-2.0', 'llm', 'https://api.siliconflow.cn/v1/', now(), now(), NULL),
  ('a5f54e274bec4e30ac844ce89eba86bb', 'f756580d5758487ea88691268169308c', 'SiliconFlow', 'inclusionAI/Ling-flash-2.0', 'llm', 'https://api.siliconflow.cn/v1/', now(), now(), NULL),
  ('d8467ed47f0d46e0a575f28ec32445fc', 'f756580d5758487ea88691268169308c', 'SiliconFlow', 'inclusionAI/Ling-mini-2.0', 'llm', 'https://api.siliconflow.cn/v1/', now(), now(), NULL),
  ('0b58810a684a4727bafc46aac3f7048e', 'f756580d5758487ea88691268169308c', 'SiliconFlow', 'Qwen/Qwen3-Coder-30B-A3B-Instruct', 'llm', 'https://api.siliconflow.cn/v1/', now(), now(), NULL),
  ('7bf8d5e171d047c19dd87068130dc65f', 'f756580d5758487ea88691268169308c', 'SiliconFlow', 'Qwen/Qwen3-30B-A3B-Thinking-2507', 'llm', 'https://api.siliconflow.cn/v1/', now(), now(), NULL),
  ('a7d8a18b04dd48b68e1de198262e20f8', 'f756580d5758487ea88691268169308c', 'SiliconFlow', 'Qwen/Qwen3-30B-A3B-Instruct-2507', 'llm', 'https://api.siliconflow.cn/v1/', now(), now(), NULL),
  ('b9aee44c98cf42beb2f4cc97b811acf3', 'f756580d5758487ea88691268169308c', 'SiliconFlow', 'Qwen/Qwen3-235B-A22B-Instruct-2507', 'llm', 'https://api.siliconflow.cn/v1/', now(), now(), NULL),
  ('17a47e16caf140f3a06c95e579e7902e', 'f756580d5758487ea88691268169308c', 'SiliconFlow', 'THUDM/GLM-4.1V-9B-Thinking', 'VLM', 'https://api.siliconflow.cn/v1/', now(), now(), NULL),
  ('229bb2e63be74160b1299620fc08f4ab', 'f756580d5758487ea88691268169308c', 'SiliconFlow', 'tencent/Hunyuan-A13B-Instruct', 'llm', 'https://api.siliconflow.cn/v1/', now(), now(), NULL),
  ('5994dd38ad0440bdbdf4722d215d0b59', 'f756580d5758487ea88691268169308c', 'SiliconFlow', 'deepseek-ai/DeepSeek-R1-0528-Qwen3-8B', 'llm', 'https://api.siliconflow.cn/v1/', now(), now(), NULL),
  ('972cfd3f287b41d09a3bdd164288f01e', 'f756580d5758487ea88691268169308c', 'SiliconFlow', 'Qwen/Qwen3-32B', 'llm', 'https://api.siliconflow.cn/v1/', now(), now(), NULL),
  ('384d17e473424e3a93cda5cf14379159', 'f756580d5758487ea88691268169308c', 'SiliconFlow', 'Qwen/Qwen3-14B', 'llm', 'https://api.siliconflow.cn/v1/', now(), now(), NULL),
  ('c1890749b8664d6fb673dbdffea006a3', 'f756580d5758487ea88691268169308c', 'SiliconFlow', 'Qwen/Qwen3-8B', 'llm', 'https://api.siliconflow.cn/v1/', now(), now(), NULL),
  ('862b2fde94e04b8792fdb5b8a41b91a3', 'f756580d5758487ea88691268169308c', 'SiliconFlow', 'encent/Hunyuan-MT-7B', 'llm', 'https://api.siliconflow.cn/v1/', now(), now(), NULL),
  ('e8d3fb6324c94d4e9308356297e64b41', 'f756580d5758487ea88691268169308c', 'SiliconFlow', 'ByteDance-Seed/Seed-OSS-36B-Instruct', 'VLM', 'https://api.siliconflow.cn/v1/', now(), now(), NULL),
  ('1bf440a3d8e84547b371b5dc7eff0216', 'f756580d5758487ea88691268169308c', 'SiliconFlow', 'zai-org/GLM-4.5V', 'VLM', 'https://api.siliconflow.cn/v1/', now(), now(), NULL),
  ('dae08d6a91a444509fd4a102f6bc0be7', 'f756580d5758487ea88691268169308c', 'SiliconFlow', 'zai-org/GLM-4.5-Air', 'llm', 'https://api.siliconflow.cn/v1/', now(), now(), NULL),
  ('858c5a25243b4d12b2d3e4fec06e05aa', 'f756580d5758487ea88691268169308c', 'SiliconFlow', 'Qwen/Qwen2.5-72B-Instruct-128K', 'llm', 'https://api.siliconflow.cn/v1/', now(), now(), NULL),
  ('b96d8e1e69fd49d28cf9126bceb53cb3', 'f756580d5758487ea88691268169308c', 'SiliconFlow', 'Qwen/Qwen2.5-72B-Instruct', 'llm', 'https://api.siliconflow.cn/v1/', now(), now(), NULL),
  ('abe7b2a048a047e7a363530649264b20', 'f756580d5758487ea88691268169308c', 'SiliconFlow', 'Qwen/Qwen2.5-32B-Instruct', 'llm', 'https://api.siliconflow.cn/v1/', now(), now(), NULL),
  ('d159576ed91344019039f10994e0366c', 'f756580d5758487ea88691268169308c', 'SiliconFlow', 'Qwen/Qwen2.5-14B-Instruct', 'llm', 'https://api.siliconflow.cn/v1/', now(), now(), NULL),
  ('7acb670660ce44609c426f8ece1920de', 'f756580d5758487ea88691268169308c', 'SiliconFlow', 'Qwen/Qwen2.5-7B-Instruct', 'llm', 'https://api.siliconflow.cn/v1/', now(), now(), NULL),
  ('dbaad1e1ad01447e88229a3b288fdd7f', 'f756580d5758487ea88691268169308c', 'SiliconFlow', 'Pro/Qwen/Qwen2.5-7B-Instruct', 'llm', 'https://api.siliconflow.cn/v1/', now(), now(), NULL),
  ('b658d85406644669ba6a138d3629e335', 'f756580d5758487ea88691268169308c', 'SiliconFlow', 'Qwen/Qwen3-VL-Embedding-8B', 'multimodal_embedding', 'https://api.siliconflow.cn/v1/embeddings', now(), now(), NULL),
  ('53fabf7699ea4bc6b34adabdf02f0b36', 'f756580d5758487ea88691268169308c', 'SiliconFlow', 'Qwen/Qwen3-Embedding-8B', 'embedding', 'https://api.siliconflow.cn/v1/embeddings', now(), now(), NULL),
  ('a8701e14b62648e5903eec60cd70fdb5', 'f756580d5758487ea88691268169308c', 'SiliconFlow', 'Qwen/Qwen3-Embedding-4B', 'embedding', 'https://api.siliconflow.cn/v1/embeddings', now(), now(), NULL),
  ('ffc337b4b5624ef6848ae1c6bf13ee3e', 'f756580d5758487ea88691268169308c', 'SiliconFlow', 'Qwen/Qwen3-Embedding-0.6B', 'embedding', 'https://api.siliconflow.cn/v1/embeddings', now(), now(), NULL),
  ('b08ad904e78e44c594d0929c6d7f7250', 'f756580d5758487ea88691268169308c', 'SiliconFlow', 'BAAI/bge-m3', 'embedding', 'https://api.siliconflow.cn/v1/embeddings', now(), now(), NULL),
  ('5725f259583b4ab3979c4e3644132985', 'f756580d5758487ea88691268169308c', 'SiliconFlow', 'BAAI/bge-large-zh-v1.5', 'embedding', 'https://api.siliconflow.cn/v1/embeddings', now(), now(), NULL),
  ('331e8a9f189c42ae8bb8dff75bf7c72d', 'f756580d5758487ea88691268169308c', 'SiliconFlow', 'BAAI/bge-large-en-v1.5', 'embedding', 'https://api.siliconflow.cn/v1/embeddings', now(), now(), NULL),
  ('2a924faf1fb748358fc97c62d3860b3b', 'f756580d5758487ea88691268169308c', 'SiliconFlow', 'netease-youdao/bce-embedding-base_v1', 'embedding', 'https://api.siliconflow.cn/v1/embeddings', now(), now(), NULL),
  ('b91e1f43513b47f2aae3031a73cf9c17', 'f756580d5758487ea88691268169308c', 'SiliconFlow', 'Pro/BAAI/bge-m3', 'embedding', 'https://api.siliconflow.cn/v1/embeddings', now(), now(), NULL),
  ('6d508975f66545068bb8002e9b4516bb', 'f756580d5758487ea88691268169308c', 'SiliconFlow', 'Qwen/Qwen3-VL-Reranker-8B', 'rerank', 'https://api.siliconflow.cn/v1/rerank', now(), now(), NULL),
  ('b0f1c8e10cd24c33a55313990b8f6320', 'f756580d5758487ea88691268169308c', 'SiliconFlow', 'Qwen/Qwen3-Reranker-8B', 'rerank', 'https://api.siliconflow.cn/v1/rerank', now(), now(), NULL),
  ('d5472c3341f04dffb40e142be5f899dc', 'f756580d5758487ea88691268169308c', 'SiliconFlow', 'Qwen/Qwen3-Reranker-4B', 'rerank', 'https://api.siliconflow.cn/v1/rerank', now(), now(), NULL),
  ('2aa37cbe3b5144c5a30c314d58c108b0', 'f756580d5758487ea88691268169308c', 'SiliconFlow', 'Qwen/Qwen3-Reranker-0.6B', 'rerank', 'https://api.siliconflow.cn/v1/rerank', now(), now(), NULL),
  ('e47e105f854c4b0280ef56c182fd3251', 'f756580d5758487ea88691268169308c', 'SiliconFlow', 'BAAI/bge-reranker-v2-m3', 'rerank', 'https://api.siliconflow.cn/v1/rerank', now(), now(), NULL),
  ('64ca86a03bb145049ea2e6dc354bbe14', 'f756580d5758487ea88691268169308c', 'SiliconFlow', 'netease-youdao/bce-reranker-base_v1', 'rerank', 'https://api.siliconflow.cn/v1/rerank', now(), now(), NULL),
  ('ed105ad7fa82434dbee191d35785489b', 'f756580d5758487ea88691268169308c', 'SiliconFlow', 'Pro/BAAI/bge-reranker-v2-m3', 'rerank', 'https://api.siliconflow.cn/v1/rerank', now(), now(), NULL),
  ('7c19aee5dabf47bdb34b3154ae489541', 'f756580d5758487ea88691268169308c', 'SiliconFlow', 'Qwen/Qwen-Image-Edit-2509', 'text2image', 'https://api.siliconflow.cn/v1/', now(), now(), NULL),
  ('c9fed87f280243ee9174adde1dece4d7', 'f756580d5758487ea88691268169308c', 'SiliconFlow', 'Qwen/Qwen-Image-Edit', 'text2image', 'https://api.siliconflow.cn/v1/', now(), now(), NULL),
  ('e692eddaeee544b3beb0395d55a60860', 'f756580d5758487ea88691268169308c', 'SiliconFlow', 'Qwen/Qwen-Image', 'text2image', 'https://api.siliconflow.cn/v1/', now(), now(), NULL),
  ('f8e033c637874ac48045109af3711020', 'f756580d5758487ea88691268169308c', 'SiliconFlow', 'Kwai-Kolors/Kolors', 'text2image', 'https://api.siliconflow.cn/v1/', now(), now(), NULL),
  ('0b280030fde34acfbad2246858906f29', 'f756580d5758487ea88691268169308c', 'SiliconFlow', 'fnlp/MOSS-TTSD-v0.5', 'tts', 'https://api.siliconflow.cn/v1/', now(), now(), NULL),
  ('253832ddedc8420cacec88b1d681ebbc', 'f756580d5758487ea88691268169308c', 'SiliconFlow', 'FunAudioLLM/CosyVoice2-0.5B', 'tts', 'https://api.siliconflow.cn/v1/', now(), now(), NULL),
  ('fc0b7caecce14506acb66d4ac48a12af', 'f756580d5758487ea88691268169308c', 'SiliconFlow', 'FunAudioLLM/SenseVoiceSmall', 'stt', 'https://api.siliconflow.cn/v1/', now(), now(), NULL),
  ('8d410aa31c604a3dbcd19df680b302ac', 'f756580d5758487ea88691268169308c', 'SiliconFlow', 'TeleAI/TeleSpeechASR', 'stt', 'https://api.siliconflow.cn/v1/', now(), now(), NULL),
  ('83d49b2566b4403ebe165942970405b7', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'qwen-image-2.0-pro', 'text2image', 'https://dashscope.aliyuncs.com/', now(), now(), NULL),
  ('b0f92ee0596f4531975f3523fa15c52f', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'qwen-image-2.0-pro-2026-04-22', 'text2image', 'https://dashscope.aliyuncs.com/', now(), now(), NULL),
  ('d597fcc32fc344b2a93d5664892ba170', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'qwen-image-2.0-pro-2026-03-03', 'text2image', 'https://dashscope.aliyuncs.com/', now(), now(), NULL),
  ('bfd1bdf1aa9f4e21b817294f315da1eb', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'qwen-image-2.0', 'text2image', 'https://dashscope.aliyuncs.com/', now(), now(), NULL),
  ('f3e977c4793742d1b362e24dfe143e15', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'qwen-image-2.0-2026-03-03', 'text2image', 'https://dashscope.aliyuncs.com/', now(), now(), NULL),
  ('5b0b02fb3b6f4fd6bdd426bf33f42fd5', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'qwen-image-max', 'text2image', 'https://dashscope.aliyuncs.com/', now(), now(), NULL),
  ('43820480007646c49c63b5cae5441faa', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'qwen-image-max-2025-12-30', 'text2image', 'https://dashscope.aliyuncs.com/', now(), now(), NULL),
  ('fc1bc63ee308472298eec8774a6a92f6', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'qwen-image-plus', 'text2image', 'https://dashscope.aliyuncs.com/', now(), now(), NULL),
  ('16760d98201d4cd59d1f67bb244bd361', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'qwen-image-plus-2026-01-09', 'text2image', 'https://dashscope.aliyuncs.com/', now(), now(), NULL),
  ('b17796ba24e94fbf9dbe5ecd2bf68ebc', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'qwen-image', 'text2image', 'https://dashscope.aliyuncs.com/', now(), now(), NULL),
  ('925fa1b199e3452b985ef88a0e1553fa', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'z-image-turbo', 'text2image', 'https://dashscope.aliyuncs.com/', now(), now(), NULL),
  ('1f4bbc6399804f22815ccb92e25c9ec4', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'wan2.5-t2i-preview', 'text2image', 'https://dashscope.aliyuncs.com/', now(), now(), NULL),
  ('f865f5b111fb48a3b7774863780ff5f1', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'wan2.2-t2i-plus', 'text2image', 'https://dashscope.aliyuncs.com/', now(), now(), NULL),
  ('d569365c08624b0182506f96d4e883ef', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'wan2.2-t2i-flash', 'text2image', 'https://dashscope.aliyuncs.com/', now(), now(), NULL),
  ('4a2da305c1ae42cf9ef15d92bbc2429c', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'wanx2.1-t2i-plus', 'text2image', 'https://dashscope.aliyuncs.com/', now(), now(), NULL),
  ('954ec99fad044b9fb205e947b8af86cc', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'wanx2.0-t2i-turbo', 'text2image', 'https://dashscope.aliyuncs.com/', now(), now(), NULL),
  ('3dd5cc0d29e3414d9e42752ddea725a1', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'wanx-v1', 'text2image', 'https://dashscope.aliyuncs.com/', now(), now(), NULL),
  ('cf7a4d52c97440faae6c806801df567e', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'qwen-image-edit-max', 'image_editing', 'https://dashscope.aliyuncs.com/', now(), now(), NULL),
  ('cac19b61cc8e454db4eb85d3e0f1a5c8', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'qwen-image-edit-max-2026-01-16', 'image_editing', 'https://dashscope.aliyuncs.com/', now(), now(), NULL),
  ('459fff98287047c7b5e39e9e872eb143', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'qwen-image-edit-plus', 'image_editing', 'https://dashscope.aliyuncs.com/', now(), now(), NULL),
  ('0cf4118a65d24897839f56d62e1be3ca', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'qwen-image-edit-plus-2025-12-15', 'image_editing', 'https://dashscope.aliyuncs.com/', now(), now(), NULL),
  ('0eeaec86ed584a8abd0d1f1270f9226e', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'qwen-image-edit-plus-2025-10-30', 'image_editing', 'https://dashscope.aliyuncs.com/', now(), now(), NULL),
  ('6ed532e5e56c4ce89cf56bd7a9f1de2f', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'qwen-image-edit', 'image_editing', 'https://dashscope.aliyuncs.com/', now(), now(), NULL),
  ('dde066922cc4459caea2971aab90b249', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'wan2.7-image-pro', 'image_editing', 'https://dashscope.aliyuncs.com/', now(), now(), NULL),
  ('a7316d879d7246b4adb2409e80e54a94', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'wan2.7-image', 'image_editing', 'https://dashscope.aliyuncs.com/', now(), now(), NULL),
  ('7232b164231c4e42ab0f848ad68a5106', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'wan2.6-image', 'image_editing', 'https://dashscope.aliyuncs.com/', now(), now(), NULL),
  ('fa9245a94f784fccb48ff58102adc0e0', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'wan2.5-i2i-preview', 'image_editing', 'https://dashscope.aliyuncs.com/', now(), now(), NULL),
  ('86432a0c8be142a0bdff87f998230106', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'wanx2.1-imageedit', 'image_editing', 'https://dashscope.aliyuncs.com/', now(), now(), NULL),
  ('23b8f7e76b5d46edb73360158afb9737', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'wanx-sketch-to-image-lite', 'image_editing', 'https://dashscope.aliyuncs.com/', now(), now(), NULL),
  ('c5d3a14958944783b761782182f16aa3', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'wanx-style-repaint-v1', 'image_editing', 'https://dashscope.aliyuncs.com/', now(), now(), NULL),
  ('ec2453a635304410ac28544a2fd6d3f1', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'wanx-background-generation-v2', 'image_editing', 'https://dashscope.aliyuncs.com/', now(), now(), NULL),
  ('62195432d8b246778b0afc88dbe895a4', '7ad36d2989c14f158a8c5f346d1054a8', 'Qwen', 'image-out-painting', 'image_editing', 'https://dashscope.aliyuncs.com/', now(), now(), NULL);
