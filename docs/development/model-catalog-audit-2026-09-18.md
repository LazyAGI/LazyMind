# Model catalog audit — 2026-09-18

Scope: all 400 pre-existing model entries across 11 model suppliers, including
speech, image, video, embedding and reranking entries. Public provider model
lists, individual model cards and lifecycle announcements were checked; no
paid inference probes were made. This is a dated documentation audit, not an
account-specific availability guarantee.

`vision: true` means the chat model accepts image input. It does not imply audio,
video or arbitrary file support. Existing `type: vlm` entries retain their role.
New general multimodal chat models use `type: llm` with `vision: true`.

## Coverage and changes

| Supplier | Before | Removed/replaced | Added | After | Official catalog |
| --- | ---: | ---: | ---: | ---: | --- |
| Claude | 3 | 0 | 3 | 6 | [Source](https://platform.claude.com/docs/en/models/overview) |
| DeepSeek | 2 | 1 | 1 | 2 | [Source](https://api-docs.deepseek.com/) |
| Doubao | 33 | 0 | 8 | 41 | [Source](https://docs.volcengine.com/docs/ark/model-list?lang=zh) |
| GLM | 32 | 0 | 4 | 36 | [Source](https://docs.bigmodel.cn/cn/guide/start/model-overview) |
| Kimi | 13 | 12 | 3 | 4 | [Source](https://platform.kimi.com/docs/models) |
| Minimax | 13 | 0 | 1 | 14 | [Source](https://platform.minimax.io/docs/guides/models-intro) |
| OpenAI | 28 | 1 | 4 | 31 | [Source](https://developers.openai.com/api/docs/models) |
| OpenRouter | 40 | 1 | 3 | 42 | [Source](https://openrouter.ai/api/v1/models) |
| Qwen | 122 | 15 | 4 | 111 | [Source](https://help.aliyun.com/zh/model-studio/models) |
| SenseNova | 30 | 0 | 0 | 30 | [Source](https://www.sensecore.cn/help/docs/model-as-a-service/nova/overview/compatible-mode) |
| SiliconFlow | 84 | 17 | 25 | 92 | [Source](https://www.siliconflow.cn/models) |

## Removal decisions

- Kimi: remove 12 retired K2/K2.5 and moonshot-v1 entries; preserve K2.6.
  Add K3 and both K2.7 Code variants. [Lifecycle and current models](https://platform.kimi.com/docs/models).
- SiliconFlow: remove 17 entries explicitly retired on May 15, June 11 or
  September 11, 2026. Do not apply these retirements to other hosting providers.
  [Announcements](https://docs.siliconflow.cn/docs/release-notes/overview).
- Qwen: remove 15 IDs in the provider's already-retired table, including old
  Qwen2.5, small Qwen3 and preview reasoning models.
  [Retired model table](https://help.aliyun.com/zh/model-studio/rate-limit).
- DeepSeek: replace `deepseek-v4-flash` with recommended `deepseek-flash`, which
  accepts images. Keep `deepseek-v4-pro`, whose service continues.
  [Official API guide](https://api-docs.deepseek.com/).
- OpenRouter: replace the Qwen3.8 Max alias with the listed
  `qwen/qwen3.8-max-0902` ID. Preserve media entries absent from the chat-model
  listing: their individual provider pages still exist. Remove the free-auto-
  selection marker from paid `z-ai/glm-5.3-flash`.
  [Live metadata](https://openrouter.ai/api/v1/models).
- OpenAI: remove `rerank-multilingual-v3.0`, which belongs to Cohere rather than
  OpenAI. [Cohere Rerank](https://docs.cohere.com/docs/rerank).
  Future retirement notices are not treated as completed shutdowns.
  [OpenAI deprecations](https://developers.openai.com/api/docs/deprecations).

## Retained uncertainty

Older does not mean retired. Models labelled legacy, scheduled for a future
shutdown, or absent from a public listing without an explicit retirement notice
are retained. In particular, CharGLM-4, TeleAI/TeleSpeechASR, and older SenseNova
hosted models were not removed on the basis of missing public documentation.
OpenRouter's public model listing is not an exhaustive list of media endpoints.

## Sync behavior

Startup catalog seeding removes omitted defaults from the default catalog and
from inherited model rows on the supplier's official endpoint. It also removes
selections referring to those deleted rows. Manually added models, custom
endpoints, other suppliers, and empty catalog sections are preserved. SenseNova
continues to separate classic and Token Plan endpoints. No live database is
modified until normal startup seeding runs.

Historical context-window aliases remain valid for manually configured models.
No runtime capability probing or service restart is part of this change.

## Inventory

[Full before/after inventory](model-catalog-audit-2026-09-18.csv) records every
original and added entry, its configured role/image capability, action and
provider sources. `image_input` describes the resulting catalog configuration,
not a claim that an account-specific inference request was executed.
