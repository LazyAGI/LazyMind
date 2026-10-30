---
name: workflow
description: Start or continue a LazyMind Workflow, execute authorized steps, use its embedded panel, and yield for human review.
---

Use the plugin's LazyMind MCP tools to discover and start the selected workflow.

Before workflow.start, ask only for missing required inputs. With request_user_input_async, accepted means displayed: keep the turn active and use an interruptible wait until the user answers. If the tool is unavailable or fails, ask in plain text.

Use an explicit unique idempotency_key of 16–256 characters and preserve it across start retries. The host connector resolves the native conversation identity. Never guess an ID or use the Workflow session_id as driver_session_id; if identity is unavailable, follow the returned host guidance and retry the same start.
The host displays the Workflow panel automatically at the end of the bound chat. Use that panel for review and control; do not open a browser panel or add a panel link to your reply.
Read workflow.state before advancing. Claim an existing execution_id; begin a ready step only when Core permits it.
When Core reports awaiting_user, tell the user to review in the panel and END this turn. Do not approve, begin another step, or poll while waiting.
On awaiting_executor, stopped, completed, or binding_required, also end this turn. For completed, deliver the artifact links.
When a queued continuation arrives, read the latest Core state first. The notification is not execution authorization.
Stop is handled by Core and the host connector. Do not replace user Panel actions with your own approvals.
