# Model inference probes

`POST /api/model/probe` runs one small inference request for a saved model. Core
owns authorization, credential selection and the saved configuration; this package
does not change keys, provider verification, default models or model metadata.

- `contracts.py`: internal input and request plan contracts. Credentials are excluded from `ProbeInput` representations.
- `profiles.py`: explicit model type and provider request shapes. There is no fallback from an unknown type to chat.
- `fixtures.py`: deterministic in-memory PNG and silent WAV inputs; no user files or temporary files.
- `runner.py`: bounded asynchronous HTTP transport, response validation and safe result codes.
- `../api/model_probe_routes.py`: request validation and the internal HTTP boundary.

| Model type | Minimum request |
| --- | --- |
| `llm` / `vlm` | One short chat message; vision also sends a small PNG. Anthropic uses Messages. |
| `embed` | One short text sent to Embeddings. |
| `multimodal_embedding` | One PNG sent to an explicitly supported provider's embedding endpoint. |
| `rerank` | One query and one document. |
| `text2image` | One short image generation prompt. |
| `image_editing` | One PNG and a short edit instruction. |
| `text2video` | Create one short video generation job. |
| `tts` | Synthesize “OK”; validate the returned audio bytes. |
| `stt` | Transcribe a short silent WAV. An empty text string is valid. |

OpenAI-compatible services (including VLLM), Anthropic and OpenRouter have explicit
profiles. Native Qwen, Doubao, SenseNova and SiliconFlow shapes are implemented only
where the request contract is known. Unsupported provider/type combinations return
`unsupported_provider` without making an HTTP request. OpenRouter reranking,
multimodal embedding and image editing stay unsupported until the application's
runtime adapters support them. Native Qwen embedding/reranking accepts an origin
or an `/api/v1/` base; native SenseNova embedding requires a `/v1/llm/` base. Custom
origins and proxy prefixes are preserved rather than replaced by a public host.

OpenRouter TTS requests MP3 with a verified model/voice pair, because its endpoint
supports MP3/PCM and many models require an explicit voice. The supported pairs
are Voxtral Mini TTS 2603 / `en_paul_neutral`, Grok Voice TTS 1.0 / `eve`, MAI-Voice-2
and 2.1 / the matching Harper Azure voice, GPT-4o Mini TTS 2025-12-15 / `alloy`, and
Gemini 3.8 Flash Lite TTS / `Kore`. Other OpenRouter TTS model IDs return
`unsupported_provider` before any request until their voice contract is verified.
See the [OpenRouter TTS guide](https://openrouter.ai/docs/guides/overview/multimodal/tts)
and [official tutorial](https://openrouter.ai/blog/tutorials/text-to-speech/).

There is one request, no retry and no redirect. Connection timeout is 5 seconds,
read timeout is 30 seconds, and an overall 35-second deadline cancels and closes the
actual async request. Responses are capped at 2 MiB for JSON and 8 MiB for audio;
compressed responses are rejected to keep the cap effective before decompression.
Generated media URLs are validated but never fetched. No response body, key, URL,
fixture or generated output is returned to the frontend.

`passed` means the type-specific response was valid. `accepted` means a video task
was created; it does not claim that generation finished. Probes never poll video
jobs. Provider failures become fixed safe codes. The tests use `httpx.MockTransport`
and never contact a real provider or consume an account's credits.
