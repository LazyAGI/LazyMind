---
name: workflow
description: Start or continue a LazyMind Workflow, execute authorized steps, open its panel, and yield for human review.
---

Use the plugin's LazyMind MCP tools to discover and start the selected workflow.

Before workflow.start, ask only for missing required inputs. With request_user_input_async, accepted means displayed: keep the turn active and use an interruptible wait until the user answers. If the tool is unavailable or fails, ask in plain text.

If workflow.start needs driver_session_id, read CODEX_THREAD_ID from the current host shell and pass the exact UUID. Never guess an ID. Use a stable idempotency_key across start retries.
Open the returned interaction_url with the host open_in_codex tool in the bottom browser panel.
Read workflow.state before advancing. Claim an existing execution_id; begin a ready step only when Core permits it.
When Core reports awaiting_user, tell the user to review in the panel and END this turn. Do not approve, begin another step, or poll while waiting.
On awaiting_executor, stopped, completed, or binding_required, also end this turn. For completed, deliver the artifact links.
When a continuation notification arrives, read the latest Core state first. The notification is not execution authorization.
Stopping a Workflow revokes Core authorization. Shared App Server mode can interrupt a verified Workflow-owned Codex turn; legacy queue mode cannot. A turn interruption does not guarantee termination of an already-started external command. Never assume a stop succeeded without its reported result.
