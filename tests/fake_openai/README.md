# Fake OpenAI-compatible server

This is a local-only test server for checking LazyMind's frontend and runtime
display of model terminal outcomes and upstream HTTP errors. It does not call
DeepSeek, MiniMax, OpenAI, or any other external service.

Start it on the host:

```bash
python3 tests/fake_openai/server.py --host 0.0.0.0 --port 18081
```

Configure a temporary OpenAI-compatible model in LazyMind with:

```text
base_url: http://host.docker.internal:18081/v1/
api_key: fake-key
```

The `model` field is only echoed in the response. The server chooses behavior
from the latest user query, using a marker such as `[fake:length]`.

## Scenarios

| Query marker | Response |
| --- | --- |
| `[fake:stop]` | 200, normal response, `finish_reason=stop` |
| `[fake:length]` | 200, partial content, `finish_reason=length` |
| `[fake:content_filter]` | 200, empty/filtered content, `finish_reason=content_filter` |
| `[fake:sensitive_input]` | HTTP 400 with no recognized safety business code; OpenAI source treats it as `invalid_request` |
| `[fake:sensitive_output]` | 200, filtered output, `finish_reason=content_filter` |
| `[fake:tool_calls]` | 200, an OpenAI function tool call, `finish_reason=tool_calls` |
| `[fake:insufficient_system_resource]` | 200, DeepSeek-style terminal reason |
| `[fake:unknown_finish]` | 200, non-standard terminal reason for unknown handling |
| `[fake:400]` | HTTP 400 invalid request |
| `[fake:401]` | HTTP 401 authentication failure |
| `[fake:402]` | HTTP 402 with no recognized Provider business code |
| `[fake:422]` | HTTP 422 invalid parameters |
| `[fake:429]` | Bare HTTP 429 with `Retry-After: 2`; expected normalized `rate_limited` |
| `[fake:429_quota]` / `[fake:429_balance]` | HTTP 429 with OpenAI `credit_balance_exhausted` |
| `[fake:429_org_spend]` | HTTP 429 with OpenAI `organization_spend_limit_exceeded` |
| `[fake:429_project_spend]` | HTTP 429 with OpenAI `project_spend_limit_exceeded` |
| `[fake:429_org_usage]` | HTTP 429 with OpenAI `organization_usage_limit_exceeded`; expected normalized `usage_limit_exceeded` |
| `[fake:429_quota_type]` | HTTP 429 with only `error.type=insufficient_quota` |
| `[fake:429_unknown]` | Alias of the bare HTTP 429 scenario |
| `[fake:500]` | HTTP 500 server error |
| `[fake:503]` | HTTP 503 server overloaded, with `Retry-After: 2` |
| `[fake:protocol_json]` | HTTP 200 with malformed JSON |
| `[fake:protocol_sse]` | SSE with malformed JSON frame |
| `[fake:eof]` | Partial SSE stream ending without terminal frame or `[DONE]` |
| `[fake:minimax_1002]` | HTTP 200 with MiniMax `base_resp.status_code=1002` |
| `[fake:minimax_1008]` | HTTP 200 with MiniMax `base_resp.status_code=1008` |
| `[fake:minimax_1026]` | HTTP 200 with MiniMax `base_resp.status_code=1026` |
| `[fake:minimax_1027]` | HTTP 200 with MiniMax `base_resp.status_code=1027` |

For example:

```text
请测试 [fake:429]
```

`Retry-After: 2` is included to confirm that HTTP failures are not retried and
that transport metadata is not exposed to the browser. The error body's shape
follows the common OpenAI-compatible envelope. Only the billing codes listed
above represent the OpenAI contract exercised by this fixture.

The service also supports `/health`, `/v1/models`, streaming and non-streaming
`/v1/chat/completions` requests.
