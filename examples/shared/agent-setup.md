# Connect your existing Agent

[中文](agent-setup.CN.md) · [Examples](../README.md)

## One-time setup

1. Install and start LazyMind using the [quick start](../../docs/quick_start.md), then sign in. Use the same instance throughout setup and the example.
2. Configure and verify the services the case needs. A PPT workflow and a knowledge search have different model/tool requirements; a healthy HTTP endpoint alone is insufficient.
3. In LazyMind settings, open the external Agent integration section and connect **Codex** or **DeepSeek Harness**. Use the connector's installation flow; do not copy tokens into a chat or manually paste internal IDs.
4. Start a new task in the connected Agent. Ask it to list the relevant LazyMind capabilities before running the case. Do not disconnect an existing integration pointing to a different instance merely to run a demo; resolve that ownership conflict first.

| Host | Installation reference | Review interaction |
| --- | --- | --- |
| Codex Desktop | [Codex Workflow plugin](../../integrations/codex-workflow/README.md) | The integration asks the host to open the returned Workflow page; an explicit link is the fallback if opening is unavailable |
| DeepSeek Harness | [DSH Workflow plugin](../../integrations/dsh-workflow/README.md) | The plugin displays a session-scoped Workflow panel |

The Codex source integration requires initialized Desktop Codex and Node.js >= 22.19. The DSH integration documents SDK baseline 0.1.6-alpha.1. Record the actual installed versions; those source requirements are not proof of a tested release combination.

## Capabilities to check

- **PPT:** the published PPT Workflow is discoverable, its input material is readable, and its required generation/export services are available. A tool list alone does not test execution.
- **Knowledge:** `knowledge.list`, `knowledge.search`, and `knowledge.document.get` must be available. Select the intended knowledge base by name and confirm it if names are ambiguous. Search returns evidence; the external Agent writes the answer.

During review, the user operates the panel. Do not ask the Agent to approve on the user's behalf. Codex and DSH differ in host control: in particular, Codex Stop does not guarantee interruption of a native tool already running. A returned link or queued notification is not proof that the page opened or the Agent resumed.

## Separate setup from daily use

Record first-time installation, model configuration and material ingestion separately from recurring task prompts, reviews, file transfers and edits. Include failures, retries, waiting and manual corrections. Do not claim reduced effort without showing what the user stopped having to do.

For a comparison, let the same Agent use the same material directly with its normal file tools and existing skills. Keep the model and task comparable, or disclose the differences. Do not compare LazyMind against an artificially restricted baseline.

## Source checkout validation

Use an isolated test instance and record its actual deployed build, not just the current Git HEAD. Updating the repository does not update running binaries. Follow the checkout's deployment instructions before restarting services; preserve local settings and business data. Never publish credentials, private file paths or unredacted host configuration in example records.
