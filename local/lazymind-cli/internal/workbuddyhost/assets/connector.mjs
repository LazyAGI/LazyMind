import { open, readFile, rename, stat, unlink, writeFile } from "node:fs/promises";
import { parseArgs } from "node:util";
import { randomUUID } from "node:crypto";
import { setTimeout as setTimeout$1 } from "node:timers/promises";

//#region ../../workflow-agent-core/src/adapter.ts
/** Use only when the host definitely did not receive the input. */
var AdmissionRejected = class extends Error {};

//#endregion
//#region ../../workflow-agent-core/src/transport.ts
var BridgeError = class extends Error {
	constructor(code, message, status) {
		super(message);
		this.code = code;
		this.status = status;
	}
};
var HostBridge = class {
	base;
	constructor(url, pairing) {
		this.pairing = pairing;
		const parsed = new URL(url);
		if (!["http:", "https:"].includes(parsed.protocol) || ![
			"localhost",
			"127.0.0.1",
			"[::1]"
		].includes(parsed.hostname) || parsed.username || parsed.password || parsed.search || parsed.hash) throw new Error("Workflow Bridge must be a loopback URL without credentials");
		this.base = parsed.href.replace(/\/$/, "") + "/v1/workflow-host";
	}
	async request(path, signal, body) {
		const response = await fetch(this.base + path, {
			method: body === void 0 ? "GET" : "POST",
			signal: AbortSignal.any([signal, AbortSignal.timeout(15e3)]),
			headers: {
				Authorization: `Bearer ${this.pairing.token}`,
				"X-LazyMind-Connector-Id": this.pairing.connector_id,
				...body === void 0 ? {} : { "content-type": "application/json" }
			},
			...body === void 0 ? {} : { body: JSON.stringify(body) }
		});
		const value = await response.json();
		if (!response.ok) throw new BridgeError(typeof value.code === "string" ? value.code : "BRIDGE_ERROR", typeof value.error === "string" ? value.error : "Workflow Bridge request failed", response.status);
		return value;
	}
	async bind(runId, driver, signal) {
		return (await this.request("/bind", signal, {
			run_id: runId,
			driver_session_id: driver
		})).control;
	}
	async state(runId, signal) {
		return (await this.request(`/runs/${encodeURIComponent(runId)}/control`, signal)).control;
	}
	async actions(after, signal) {
		return this.request(`/actions${after ? `?after=${encodeURIComponent(after)}` : ""}`, signal);
	}
	async action(id, signal) {
		return this.request(`/actions/${encodeURIComponent(id)}`, signal);
	}
	async claim(id, instanceId, signal) {
		return this.request(`/actions/${encodeURIComponent(id)}/claim`, signal, { instance_id: instanceId });
	}
	async settle(id, instanceId, token, status, seq, error, signal) {
		await this.request(`/actions/${encodeURIComponent(id)}/settle`, signal, {
			instance_id: instanceId,
			dispatch_token: token,
			status,
			native_event_seq: seq,
			error
		});
	}
};

//#endregion
//#region ../../workflow-agent-core/src/dispatcher.ts
/** Polling and delivery policy are shared; only host admission/history live in the adapter. */
function createDispatcher(runtime, host, bridge, instanceId, signal) {
	async function deliver(action) {
		if (action.status === "pending" && action.kind === "continue" && runtime.ready && !await runtime.ready(action.native_session_id, signal)) return;
		if (action.status !== "pending") {
			const current = await bridge.action(action.id, signal);
			if (![
				"pending",
				"dispatching",
				"unknown"
			].includes(current.action.status)) return;
			if (current.action.status !== "pending" && action.kind === "continue") {
				const seq$1 = await runtime.reconcile?.(action.native_session_id, action.id, signal) ?? 0;
				if (seq$1 > 0) {
					await bridge.settle(action.id, instanceId, "", "accepted", seq$1, "", signal);
					return;
				}
			}
		}
		const claim = await bridge.claim(action.id, instanceId, signal);
		if (!claim.dispatch_token || claim.action.status !== "dispatching") return;
		action = claim.action;
		host?.cacheClaim(claim);
		let seq = 0;
		let admissionStarted = false;
		try {
			if (action.kind === "cancel" && runtime.cancellation === "none") throw new AdmissionRejected("This host does not support interrupting the current turn; Workflow is stopped in Core.");
			const resolved = await runtime.resolve(action.native_session_id);
			if ("error" in resolved) throw new AdmissionRejected(resolved.error);
			if (action.kind === "cancel") {
				if (!host) throw new AdmissionRejected("Session cancellation requires a host ownership guard.");
				const current = await bridge.action(action.id, signal);
				admissionStarted = true;
				await host.cancelSession(resolved.agent, current);
			} else {
				signal.throwIfAborted();
				admissionStarted = true;
				seq = await runtime.prompt(resolved.agent, {
					actionId: action.id,
					message: continuationMessage(action)
				}, signal);
			}
		} catch (error) {
			await bridge.settle(action.id, instanceId, claim.dispatch_token, !admissionStarted || error instanceof AdmissionRejected ? "failed" : "unknown", 0, String(error), signal);
			return;
		}
		await bridge.settle(action.id, instanceId, claim.dispatch_token, "accepted", seq, "", signal);
	}
	async function poll() {
		while (!signal.aborted) {
			try {
				let after = "";
				do {
					const page = await bridge.actions(after, signal);
					for (const action of [...page.actions].sort((a, b) => Number(b.kind === "cancel") - Number(a.kind === "cancel"))) try {
						await deliver(action);
					} catch (error) {
						if (!(error instanceof BridgeError && ["DELIVERY_PENDING", "ACTION_CONSUMED"].includes(error.code)) && !signal.aborted) runtime.warn(`lazymind-workflow: delivery pending: ${String(error)}`);
					}
					after = page.next_page_token ?? "";
				} while (after && !signal.aborted);
			} catch (error) {
				if (!signal.aborted) runtime.warn(`lazymind-workflow: reconnecting Bridge: ${String(error)}`);
			}
			try {
				await delay(1e3, signal);
			} catch {
				break;
			}
		}
	}
	return {
		deliver,
		poll
	};
}
function delay(ms, signal) {
	return new Promise((resolve, reject) => {
		if (signal.aborted) {
			reject(signal.reason);
			return;
		}
		const abort = () => {
			clearTimeout(timer);
			reject(signal.reason);
		};
		const timer = setTimeout(() => {
			signal.removeEventListener("abort", abort);
			resolve();
		}, ms);
		signal.addEventListener("abort", abort, { once: true });
	});
}
function continuationMessage(action) {
	const request = action.execution_id ? `When applicable, call workflow.step.claim with execution_id=${action.execution_id}. For a completed internal execution, inspect its result and the current state instead of executing it again.` : "When control.continuation=continue and admission.can_begin=true, call workflow.step.begin for a ready step.";
	return `LazyMind workflow ${action.session_id} has a control notification. Call workflow.state first; the notification may be delayed. ${request} Execute only a granted external step_contract and publish/submit using its execution_handle. If executor_host is lazymind, let Core execute it. After submission, use the returned state to continue. Yield when awaiting_user, awaiting_executor, draining, stopped, binding_required, completed, or failed; do not poll while waiting. A human step requires review AFTER execution. Do not assume this notification means a review was approved. Do not create a new workflow.`;
}

//#endregion
//#region ../../workflow-agent-core/node/runtime-lock.ts
var DispatcherBusy = class extends Error {};
/** One cooperating dispatcher per host profile pairing. */
async function dispatcherLock(pairingFile, instanceId) {
	const path = `${pairingFile}.runtime.lock`;
	for (let attempt = 0; attempt < 2; attempt++) try {
		const file = await open(path, "wx", 384);
		try {
			await file.writeFile(JSON.stringify({
				pid: process.pid,
				instanceId
			}));
		} finally {
			await file.close();
		}
		return async () => {
			try {
				if (JSON.parse(await readFile(path, "utf8")).instanceId === instanceId) await unlink(path);
			} catch (error) {
				if (error.code !== "ENOENT") throw error;
			}
		};
	} catch (error) {
		if (error.code !== "EEXIST") throw error;
		const previous = JSON.parse(await readFile(path, "utf8"));
		if (!Number.isSafeInteger(previous.pid) || previous.pid < 1) throw new Error("Invalid host dispatcher lock; repair this connection explicitly");
		try {
			process.kill(previous.pid, 0);
		} catch (error$1) {
			if (error$1.code === "ESRCH") {
				await unlink(path);
				continue;
			}
			throw error$1;
		}
		throw new DispatcherBusy("This host profile pairing already has a workflow dispatcher");
	}
	throw new Error("Could not acquire the host workflow dispatcher lock");
}

//#endregion
//#region src/compatibility.ts
function workbuddyVersionIssue(version) {
	if (typeof version !== "string") return "version_unreadable";
	const match = /^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$/.exec(version);
	if (!match || match[4]?.split(".").some((part) => /^0\d+$/.test(part))) return "version_unreadable";
	if (match.slice(1, 4).some((part) => !Number.isSafeInteger(Number(part)))) return "version_unreadable";
	const minimum = [
		5,
		8,
		3
	];
	for (let i = 0; i < minimum.length; i++) {
		const value = Number(match[i + 1]);
		if (!Number.isSafeInteger(value)) return "version_unreadable";
		if (value !== minimum[i]) return value > minimum[i] ? "" : "unsupported_version";
	}
	return match[4] ? "unsupported_version" : "";
}

//#endregion
//#region src/sdk.ts
async function sdkOperation(input, versionIssue = workbuddyVersionIssue) {
	const host = globalThis;
	const version = host.workbuddyDesktop?.appVersion;
	const sdk = host.wb?.conversations;
	const issue = versionIssue(version) || (typeof sdk?.get !== "function" || typeof sdk?.local?.list !== "function" ? "capability_unavailable" : "");
	if (input.kind === "probe") return {
		version: typeof version === "string" ? version : "",
		...issue ? { issue } : {}
	};
	if (issue) throw new Error(issue);
	if (input.kind === "identity") {
		const canonical = (v) => {
			if (Array.isArray(v)) return "[" + v.map(canonical).join(",") + "]";
			if (v && typeof v === "object") return "{" + Object.keys(v).sort().map((k) => JSON.stringify(k) + ":" + canonical(v[k])).join(",") + "}";
			return JSON.stringify(v);
		};
		const matches = [];
		const seen = /* @__PURE__ */ new Set();
		let complete = false;
		for (let page = 1; page <= 100; page++) {
			const result = await sdk.local.list({
				page,
				size: 100
			});
			if (!Array.isArray(result.items) || !Number.isFinite(result.total)) throw new Error("Unknown list format");
			let added = 0;
			for (const info of result.items) {
				if (seen.has(info.id)) continue;
				seen.add(info.id);
				added++;
				if (![
					"working",
					"pending",
					"planning"
				].includes(info.state)) continue;
				const conversation$1 = await sdk.get(info.id);
				if (typeof conversation$1?.requests !== "function") throw new Error("capability_unavailable");
				const requests = await conversation$1.requests(null);
				for (const request of requests.items) {
					if ([
						"completed",
						"cancelled",
						"error"
					].includes(request.state)) continue;
					for (const block of request.assistantMessage?.content ?? []) {
						if (block.type !== "tool" || block.status !== "in_progress" || block._meta?.["codebuddy.ai/compactTruncated"]) continue;
						let name = block.toolName ?? block._meta?.["codebuddy.ai/toolName"];
						let args = block.rawInput;
						if (name === "DeferExecuteTool") {
							name = args?.toolName;
							args = args?.params;
						}
						if (name !== "mcp__lazymind__workflow.start" || canonical(args) !== canonical(input.arguments)) continue;
						matches.push({
							sessionId: info.id,
							requestId: request.id,
							toolCallId: block.toolCallId
						});
					}
				}
			}
			if (seen.size >= result.total) {
				complete = true;
				break;
			}
			if (!added) break;
		}
		if (!complete) throw new Error("Incomplete native session inventory");
		return { matches };
	}
	let conversation;
	try {
		conversation = await sdk.get(input.sessionId);
	} catch (error) {
		if (typeof sdk.listByIds === "function") {
			const known = await sdk.listByIds([input.sessionId]);
			if (Array.isArray(known) && known.length === 0) return { missing: true };
		}
		throw error;
	}
	if (!conversation || conversation.info?.id !== input.sessionId) return { missing: true };
	if (input.kind === "status") return {
		state: conversation.info.state,
		version: host.workbuddyDesktop.appVersion
	};
	if (input.kind === "send") {
		if (conversation.info.state !== "idle") return { rejected: "Conversation became busy before admission" };
		if (typeof conversation.sendPrompt !== "function") return { rejected: "sendPrompt unavailable" };
		const result = await conversation.sendPrompt([{
			type: "text",
			text: input.message
		}], {
			clientRequestId: input.actionId,
			clientTimestamp: Date.now(),
			emitUserMessage: true
		});
		if (result?.errorCode) return { rejected: String(result.errorCode) };
		return { accepted: true };
	}
	if (input.kind === "cancel") {
		if (typeof conversation.cancel !== "function") return { rejected: "cancel unavailable" };
		const result = await conversation.cancel();
		if (result?.errorCode) return { rejected: String(result.errorCode) };
		return { accepted: true };
	}
	throw new Error("Unsupported SDK operation");
}

//#endregion
//#region src/adapter.ts
const sessionPattern = /^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$/i;
var WorkBuddyAdapter = class {
	cancellation = "session";
	constructor(cdp, log = () => {}) {
		this.cdp = cdp;
		this.log = log;
	}
	id(id) {
		return id;
	}
	warn(message) {
		this.log("connector.warning", {
			kind: message.includes("reconnecting Bridge") ? "bridge-unavailable" : "delivery-pending",
			status: "retrying"
		});
	}
	probe(signal) {
		return this.call({ kind: "probe" }, signal);
	}
	call(input, signal) {
		return this.cdp.evaluate(`(${sdkOperation.toString()})(${JSON.stringify(input)}, ${workbuddyVersionIssue.toString()})`, signal);
	}
	async admission(kind, sessionId, input, signal, fields) {
		const started = performance.now();
		try {
			if (signal.aborted) throw new AdmissionRejected("Aborted before sending");
			const result = await this.call({
				kind,
				sessionId,
				...input
			}, signal);
			if (result.missing || result.rejected) throw new AdmissionRejected(result.rejected ?? "Bound session does not exist");
			if (!result.accepted) throw new Error("Host admission is unknown");
			this.log(`native.${kind}`, {
				...fields,
				native_session_id: sessionId,
				status: "accepted",
				duration_ms: performance.now() - started
			});
		} catch (error) {
			this.log(`native.${kind}`, {
				...fields,
				native_session_id: sessionId,
				status: error instanceof AdmissionRejected ? "failed" : "unknown",
				duration_ms: performance.now() - started
			});
			throw error;
		}
	}
	async ready(sessionId, signal) {
		if (!sessionPattern.test(sessionId)) return true;
		try {
			const state = await this.call({
				kind: "status",
				sessionId
			}, signal);
			return state.missing === true || state.state === "idle";
		} catch {
			return false;
		}
	}
	async resolve(id) {
		return sessionPattern.test(id) ? { agent: id } : { error: "Native WorkBuddy session UUID required" };
	}
	async prompt(sessionId, input, signal) {
		await this.admission("send", sessionId, {
			...input,
			message: `[LazyMind action_id=${input.actionId}]\n${input.message}`
		}, signal, { action_id: input.actionId });
		return 0;
	}
	async cancel(sessionId, fields = {}) {
		if (!sessionPattern.test(sessionId)) throw new AdmissionRejected("Native WorkBuddy session UUID required");
		await this.admission("cancel", sessionId, {}, AbortSignal.timeout(1e4), fields);
	}
	control(signal) {
		return {
			cacheClaim(_claim) {},
			cancelSession: async (sessionId, { action, control }) => {
				if (action.kind !== "cancel" || action.native_session_id !== sessionId || action.consumed_at || action.status !== "dispatching" || control.session_id !== action.session_id || control.continuation !== "stopped") throw new AdmissionRejected("Cancellation no longer applies to this bound Workflow session");
				signal.throwIfAborted();
				await this.cancel(sessionId, {
					action_id: action.id,
					run_id: action.session_id,
					execution_id: action.execution_id
				});
			}
		};
	}
	async identify(args, signal) {
		const result = await this.call({
			kind: "identity",
			arguments: args
		}, signal);
		if (!Array.isArray(result.matches) || result.matches.length !== 1) throw new Error("Native workflow.start invocation is missing or ambiguous");
		return result.matches[0];
	}
};

//#endregion
//#region ../../workflow-agent-core/node/cdp.ts
/** Shared local CDP transport; each host supplies its own target identity. */
var CdpClient = class {
	constructor(port, selectTarget) {
		this.port = port;
		this.selectTarget = selectTarget;
		if (!Number.isInteger(port) || port < 1 || port > 65535) throw new Error("Invalid CDP port");
	}
	async evaluate(expression, signal, timeoutMs = 8e3) {
		const bounded = AbortSignal.any([signal, AbortSignal.timeout(timeoutMs)]);
		const response = await fetch(`http://127.0.0.1:${this.port}/json/list`, {
			signal: bounded,
			redirect: "error"
		});
		if (!response.ok) throw new Error("CDP discovery failed");
		const page = this.selectTarget(await response.json());
		const url = new URL(page.webSocketDebuggerUrl);
		if (url.protocol !== "ws:" || url.hostname !== "127.0.0.1" || Number(url.port) !== this.port || url.username || url.password) throw new Error("Unexpected CDP endpoint");
		bounded.throwIfAborted();
		const socket = new WebSocket(url);
		return await new Promise((resolve, reject) => {
			let settled = false;
			const finish = (error, value) => {
				if (settled) return;
				settled = true;
				bounded.removeEventListener("abort", abort);
				socket.close();
				error ? reject(error) : resolve(value);
			};
			const abort = () => finish(/* @__PURE__ */ new Error("CDP operation interrupted; admission may be unknown"));
			bounded.addEventListener("abort", abort, { once: true });
			socket.addEventListener("open", () => {
				if (settled) return;
				socket.send(JSON.stringify({
					id: 1,
					method: "Runtime.evaluate",
					params: {
						expression,
						awaitPromise: true,
						returnByValue: true
					}
				}));
			});
			socket.addEventListener("message", (event) => {
				try {
					const value = JSON.parse(String(event.data));
					if (value.id !== 1) return;
					if (value.error || value.result?.exceptionDetails) return finish(/* @__PURE__ */ new Error("Desktop evaluation failed"));
					finish(void 0, value.result?.result?.value);
				} catch {
					finish(/* @__PURE__ */ new Error("Invalid CDP response"));
				}
			});
			socket.addEventListener("error", () => finish(/* @__PURE__ */ new Error("CDP unavailable")));
			socket.addEventListener("close", () => finish(/* @__PURE__ */ new Error("CDP disconnected")));
			if (bounded.aborted) abort();
		});
	}
};

//#endregion
//#region src/cdp.ts
/** Loopback-only transport. Never use a page title as a conversation identity. */
function selectWorkBuddyTarget(targets) {
	const pages = targets.filter((target) => {
		if (target.type !== "page" || target.title?.toLowerCase() !== "workbuddy") return false;
		try {
			const url = new URL(target.url);
			const appId = url.searchParams.get("windowAppId") ?? url.searchParams.get("windowKind") ?? "main";
			return url.protocol === "file:" && url.pathname.endsWith("/renderer/index.html") && appId === "main";
		} catch {
			return false;
		}
	});
	if (pages.length !== 1) throw new Error("Expected one WorkBuddy desktop target");
	return pages[0];
}
var Cdp = class extends CdpClient {
	constructor(port) {
		super(port, selectWorkBuddyTarget);
	}
};

//#endregion
//#region ../../workflow-agent-core/renderer/tail-follow.ts
/** Serialized with the mount function; no module-scope dependencies. */
function installTailFollow(scroller, content, panel, initiallyFollowing, bottomButtonSelector = ".cr-scroll-to-bottom") {
	let following = initiallyFollowing, disposed = false, dragging = false, downIntent = false;
	let touchY;
	const gap = () => Math.max(0, scroller.scrollHeight - scroller.clientHeight - scroller.scrollTop);
	const atBottom = () => gap() <= 2;
	function align() {
		if (disposed) return;
		if (!panel.isConnected || panel.parentElement !== content || content.parentElement !== scroller) {
			dispose();
			return;
		}
		if (following && !dragging) {
			const end = Math.max(0, scroller.scrollHeight - scroller.clientHeight);
			if (Math.abs(scroller.scrollTop - end) > .5) scroller.scrollTop = end;
		}
	}
	function intent(delta) {
		if (delta < 0) {
			following = false;
			downIntent = false;
		}
		if (delta > 0) {
			downIntent = true;
			if (!dragging && atBottom()) following = true;
		}
	}
	function scroll() {
		if (!following && downIntent && !dragging && atBottom()) following = true;
		align();
	}
	function wheel(event) {
		if (event.ctrlKey) return;
		for (let el = event.target instanceof Element ? event.target : null; el && el !== scroller; el = el.parentElement) if (el.scrollHeight > el.clientHeight && /auto|scroll/.test(getComputedStyle(el).overflowY) && (event.deltaY < 0 ? el.scrollTop > 0 : el.scrollTop + el.clientHeight < el.scrollHeight - 1)) return;
		intent(event.deltaY);
	}
	function touchStart(event) {
		touchY = event.touches[0]?.clientY;
	}
	function touchMove(event) {
		const y = event.touches[0]?.clientY;
		if (y !== void 0 && touchY !== void 0) intent(touchY - y);
		touchY = y;
	}
	function pointerDown(event) {
		if (event.button !== 0) return;
		const rect = scroller.getBoundingClientRect();
		if (event.clientX >= rect.right - Math.max(14, scroller.offsetWidth - scroller.clientWidth)) {
			dragging = true;
			following = false;
			downIntent = false;
		}
	}
	function pointerUp() {
		if (!dragging) return;
		dragging = false;
		following = atBottom();
		align();
	}
	function keydown(event) {
		const target = event.target;
		if (!(target instanceof Element) || target.closest("input,textarea,select,[contenteditable]:not([contenteditable=\"false\"])")) return;
		if (!scroller.contains(target) && target !== document.body && target !== document.documentElement) return;
		if ([
			"ArrowUp",
			"PageUp",
			"Home"
		].includes(event.key) || event.key === " " && event.shiftKey) intent(-1);
		if (["ArrowDown", "PageDown"].includes(event.key) || event.key === " " && !event.shiftKey) intent(1);
		if (event.key === "End" && !event.shiftKey) {
			following = true;
			align();
		}
	}
	const viewport = scroller.parentElement;
	function bottomButton(event) {
		const button = event.target instanceof Element ? event.target.closest(bottomButtonSelector) : null;
		if (!button || !viewport?.contains(button)) return;
		following = true;
		downIntent = false;
		dragging = false;
		align();
	}
	const resize = typeof ResizeObserver === "function" ? new ResizeObserver(align) : null;
	resize?.observe(content);
	resize?.observe(panel);
	resize?.observe(scroller);
	const structure = new MutationObserver(align);
	structure.observe(content, { childList: true });
	scroller.addEventListener("scroll", scroll, {
		capture: true,
		passive: true
	});
	scroller.addEventListener("wheel", wheel, {
		capture: true,
		passive: true
	});
	scroller.addEventListener("touchstart", touchStart, {
		capture: true,
		passive: true
	});
	scroller.addEventListener("touchmove", touchMove, {
		capture: true,
		passive: true
	});
	scroller.addEventListener("pointerdown", pointerDown, true);
	window.addEventListener("pointerup", pointerUp, true);
	window.addEventListener("pointercancel", pointerUp, true);
	window.addEventListener("keydown", keydown, true);
	viewport?.addEventListener("mousedown", bottomButton, true);
	viewport?.addEventListener("click", bottomButton, true);
	function dispose() {
		if (disposed) return;
		disposed = true;
		resize?.disconnect();
		structure.disconnect();
		scroller.removeEventListener("scroll", scroll, true);
		scroller.removeEventListener("wheel", wheel, true);
		scroller.removeEventListener("touchstart", touchStart, true);
		scroller.removeEventListener("touchmove", touchMove, true);
		scroller.removeEventListener("pointerdown", pointerDown, true);
		window.removeEventListener("pointerup", pointerUp, true);
		window.removeEventListener("pointercancel", pointerUp, true);
		window.removeEventListener("keydown", keydown, true);
		viewport?.removeEventListener("mousedown", bottomButton, true);
		viewport?.removeEventListener("click", bottomButton, true);
	}
	align();
	return {
		dispose,
		align,
		status: () => ({
			following,
			dragging,
			disposed,
			distanceFromBottom: gap()
		})
	};
}

//#endregion
//#region src/tail-extent.ts
/** WB 5.8.3 layout adapter; serialized into its renderer with the mount. */
function installTailExtent(doc, scroller, changed) {
	function measurement() {
		const fiberKey = Object.keys(doc).find((key) => key.startsWith("__reactFiber$"));
		let fiber = fiberKey && doc[fiberKey];
		if (!fiber) return null;
		let root = fiber;
		while (root.return) root = root.return;
		if (root.stateNode?.current !== root) fiber = fiber.alternate;
		for (let depth = 0; fiber && depth < 5; depth++, fiber = fiber.return) {
			const props = fiber.memoizedProps;
			if (props?.block?.props?.rootId !== doc.dataset.rootId || !Array.isArray(props.block.children) || !props.viewport?.virtualScrollControllerRef) continue;
			let hook = fiber.memoizedState;
			for (let i = 0; hook && i < 80; i++, hook = hook.next) {
				const list = hook.memoizedState?.current;
				if (typeof list?.getTotalSize !== "function" || typeof list.getVirtualItems !== "function" || list.options?.getScrollElement?.() !== scroller || list.options.count !== props.block.children.length) continue;
				const total = list.getTotalSize();
				if (!Number.isFinite(total) || total < 0) return null;
				return {
					total,
					reserved: props.viewport.reserveTailAnchor === true,
					controller: props.viewport.virtualScrollControllerRef.current
				};
			}
		}
		return null;
	}
	const original = ["height", "overflow"].map((key) => ({
		key,
		value: doc.style.getPropertyValue(key),
		priority: doc.style.getPropertyPriority(key)
	}));
	let disposed = false, trimmed = 0, controller, unsubscribe;
	let spacer = null, flow = null;
	function restore() {
		if (!trimmed) return;
		for (const { key, value, priority } of original) if (value) doc.style.setProperty(key, value, priority);
		else doc.style.removeProperty(key);
		trimmed = 0;
	}
	function sync() {
		if (disposed) return;
		const nextSpacer = doc.querySelector(":scope > .cr-document__virtual-spacer");
		const nextFlow = nextSpacer?.querySelector(":scope > .cr-document__virtual-flow") ?? null;
		if (spacer !== nextSpacer || flow !== nextFlow) {
			resize?.disconnect();
			resize?.observe(scroller);
			spacer = nextSpacer;
			flow = nextFlow;
			if (spacer) resize?.observe(spacer);
			if (flow) resize?.observe(flow);
		}
		const measured = measurement();
		if (controller !== measured?.controller) {
			unsubscribe?.();
			controller = measured?.controller;
			unsubscribe = controller?.subscribeChange?.(sync);
		}
		const reservedHeight = spacer?.getBoundingClientRect().height ?? 0;
		const gap = measured?.reserved ? reservedHeight - measured.total : 0;
		if (measured && spacer && gap > .5 && gap <= scroller.clientHeight + 1) {
			doc.style.height = `${measured.total}px`;
			doc.style.overflow = "clip";
			trimmed = gap;
		} else restore();
		changed();
	}
	const resize = typeof ResizeObserver === "function" ? new ResizeObserver(sync) : null;
	const structure = new MutationObserver(sync);
	structure.observe(doc, {
		childList: true,
		subtree: true
	});
	sync();
	return {
		sync,
		status: () => ({
			trimmed,
			disposed
		}),
		dispose: () => {
			if (disposed) return;
			disposed = true;
			resize?.disconnect();
			structure.disconnect();
			unsubscribe?.();
			restore();
		}
	};
}

//#endregion
//#region src/presentation.ts
function readPresentationSession() {
	const host = window, sdk = host.wb?.conversations;
	if (!sdk || typeof sdk.onCurrentChange !== "function") return null;
	const key = "__lazymindWorkflowPresentationSession";
	if (host[key] && !host[key].wait) host[key].dispose();
	if (!host[key]) {
		let session = sdk.currentId || null, epoch = crypto.randomUUID();
		const waiters = /* @__PURE__ */ new Set();
		const changed = () => {
			session = sdk.currentId || null;
			epoch = crypto.randomUUID();
			for (const resolve of waiters) resolve();
		};
		const unsubscribe = sdk.onCurrentChange(changed);
		const gate = {
			read: () => {
				if (session !== (sdk.currentId || null)) changed();
				return {
					session_id: session,
					epoch
				};
			},
			wait: (expected) => {
				if (gate.read().epoch !== expected) return Promise.resolve();
				return new Promise((resolve) => {
					const finish = () => {
						clearTimeout(timer);
						waiters.delete(finish);
						resolve();
					};
					const timer = setTimeout(finish, 5e3);
					waiters.add(finish);
				});
			},
			dispose: () => {
				if (typeof unsubscribe === "function") unsubscribe();
				for (const resolve of waiters) resolve();
				if (host[key] === gate) delete host[key];
			}
		};
		host[key] = gate;
	}
	return host[key].read();
}
const cleanupPresentation = "window.__lazymindWorkflowTail?.dispose(); window.__lazymindWorkflowPresentationSession?.dispose()";
function mountPresentation(target, followTail = installTailFollow, fitTail = installTailExtent, versionIssue = workbuddyVersionIssue) {
	const host = window;
	const version = "tail-v4";
	const key = "__lazymindWorkflowTail";
	const previous = host[key];
	const wb = host.wb;
	if (target.epoch && host.__lazymindWorkflowPresentationSession?.read().epoch !== target.epoch) return { status: "stale" };
	if (!wb?.conversations) return { status: "page-not-ready" };
	const issue = versionIssue(host.workbuddyDesktop?.appVersion);
	if (issue) {
		previous?.dispose();
		return { status: issue };
	}
	if (typeof wb.conversations.onCurrentChange !== "function") {
		previous?.dispose();
		return { status: "capability_unavailable" };
	}
	if (wb?.conversations?.currentId !== target.session_id) return { status: "stale" };
	if (!target.run_id) {
		previous?.dispose();
		return { status: "unbound" };
	}
	const url = new URL(target.frame_url);
	if (url.protocol !== "http:" || !["localhost", "127.0.0.1"].includes(url.hostname) || url.username || url.password || url.pathname !== "/v1/workbuddy/frame" || url.search || url.hash || !/^[a-zA-Z0-9_-]{1,256}$/.test(target.run_id) || !/^[a-zA-Z0-9-]{20,80}$/.test(target.mount_id)) throw Error("Invalid presentation target");
	if (previous?.version === version && previous.session === target.session_id && previous.run === target.run_id && previous.origin === url.origin) {
		previous.sync();
		return previous.status();
	}
	previous?.dispose();
	host.__lazymindTailPanelDemo?.dispose();
	let cachedDoc;
	const locate = () => {
		if (wb.conversations.currentId !== target.session_id) return null;
		const doc = cachedDoc?.isConnected && cachedDoc.dataset.rootId === target.session_id && cachedDoc.getBoundingClientRect().width > 0 ? cachedDoc : Array.from(document.querySelectorAll(".cr-document[data-root-id]")).find((e) => e.dataset.rootId === target.session_id && e.getBoundingClientRect().width > 0);
		const content = doc?.parentElement;
		if (!doc || doc.getBoundingClientRect().width <= 0 || !content?.classList.contains("cr-message-list__content") || !content.parentElement?.classList.contains("cr-message-list")) return null;
		cachedDoc = doc;
		return {
			doc,
			content
		};
	};
	if (!locate()) return { status: "layout-unavailable" };
	const panel = document.createElement("section");
	panel.dataset.lazymindWorkflowTail = target.session_id;
	panel.setAttribute("aria-label", "LazyMind Workflow");
	panel.style.cssText = "box-sizing:border-box;position:relative;margin:16px auto 24px;width:100%;overflow:hidden;border-radius:12px;";
	const surface = document.createElement("div");
	surface.dataset.lazymindWorkflowSurface = "";
	const inlineStyle = "box-sizing:border-box;display:block;width:100%;margin:0;padding:0;border:0;border-radius:12px;overflow:hidden;background:white;";
	surface.style.cssText = inlineStyle;
	const frame = document.createElement("iframe");
	frame.title = "LazyMind Workflow";
	frame.style.cssText = "display:block;width:100%;border:0;background:transparent;";
	frame.setAttribute("sandbox", "allow-scripts allow-same-origin allow-forms allow-popups allow-downloads");
	url.hash = new URLSearchParams({
		run: target.run_id,
		mount: target.mount_id
	}).toString();
	frame.src = url.href;
	surface.append(frame);
	panel.append(surface);
	let disposed = false, expanded = false, collapsed = false, saving = false;
	let layoutReady = false, pageStatus = "loading";
	let observedShell = null, followedContent;
	let tailFollow;
	let tailExtent, fittedDoc;
	let syncQueued = false;
	const seen = /* @__PURE__ */ new Set();
	function size() {
		frame.style.height = expanded ? "100%" : collapsed ? "64px" : "clamp(360px, 60vh, 640px)";
	}
	function expandedBounds() {
		const shell = locate()?.doc.closest(".conversation-shell__main");
		if (!shell) return null;
		const rect = shell.getBoundingClientRect();
		const left = Math.max(0, rect.left), top = Math.max(0, rect.top);
		const right = Math.min(window.innerWidth, rect.right), bottom = Math.min(window.innerHeight, rect.bottom);
		if (right <= left || bottom <= top) return null;
		const inset = Math.min(12, (right - left) / 4, (bottom - top) / 4);
		return {
			left: left + inset,
			top: top + inset,
			width: right - left - inset * 2,
			height: bottom - top - inset * 2
		};
	}
	function setExpanded(next) {
		if (next === expanded) return;
		if (next) {
			if (!expandedBounds() || typeof surface.showPopover !== "function") return;
			panel.style.height = `${panel.getBoundingClientRect().height}px`;
			surface.setAttribute("popover", "manual");
			try {
				surface.showPopover();
			} catch {
				surface.removeAttribute("popover");
				panel.style.height = "";
				return;
			}
			expanded = true;
			collapsed = false;
			reply({
				type: "lazymind.workflow.collapse",
				collapsed
			});
		} else {
			expanded = false;
			surface.hidePopover();
			surface.removeAttribute("popover");
			surface.style.cssText = inlineStyle;
			panel.style.height = "";
		}
		size();
		sync();
	}
	function sync() {
		if (disposed) return;
		if (wb.conversations.currentId !== target.session_id) {
			dispose();
			return;
		}
		const destination = locate();
		if (!destination) {
			layoutReady = false;
			panel.hidden = true;
			tailFollow?.dispose();
			tailExtent?.dispose();
			tailExtent = void 0;
			fittedDoc = void 0;
			if (expanded) {
				setExpanded(false);
				reply({
					type: "lazymind.workflow.expansion",
					expanded
				});
			}
			return;
		}
		layoutReady = true;
		panel.hidden = false;
		const scroller = destination.content.parentElement;
		const following = tailFollow && followedContent === destination.content ? tailFollow.status().following : scroller.scrollHeight - scroller.clientHeight - scroller.scrollTop <= 24;
		panel.style.maxWidth = `${Math.round(destination.doc.getBoundingClientRect().width)}px`;
		panel.style.colorScheme = getComputedStyle(document.documentElement).colorScheme;
		if (panel.parentElement !== destination.content || panel.nextElementSibling) {
			tailFollow?.dispose();
			const move = destination.content.moveBefore;
			if (panel.isConnected && typeof move === "function") move.call(destination.content, panel, null);
			else destination.content.append(panel);
		}
		if (!tailExtent || fittedDoc !== destination.doc || followedContent !== destination.content) {
			tailExtent?.dispose();
			fittedDoc = destination.doc;
			tailExtent = fitTail(destination.doc, scroller, () => tailFollow?.align());
		} else tailExtent.sync();
		if (!tailFollow || tailFollow.status().disposed || followedContent !== destination.content) {
			tailFollow?.dispose();
			followedContent = destination.content;
			tailFollow = followTail(scroller, destination.content, panel, following);
			if (host[key]?.sync === sync) host[key].tailFollow = tailFollow;
		}
		const shell = destination.doc.closest(".conversation-shell__main");
		if (shell !== observedShell) {
			layoutObserver?.disconnect();
			structureObserver.disconnect();
			observedShell = shell;
			if (shell) {
				layoutObserver?.observe(shell);
				structureObserver.observe(shell, {
					childList: true,
					subtree: true,
					attributes: true,
					attributeFilter: ["data-root-id"]
				});
			}
		}
		if (expanded) {
			const bounds = expandedBounds();
			if (!bounds) {
				setExpanded(false);
				reply({
					type: "lazymind.workflow.expansion",
					expanded
				});
				return;
			}
			Object.assign(surface.style, {
				position: "fixed",
				inset: "auto",
				maxWidth: "none",
				maxHeight: "none",
				left: `${bounds.left}px`,
				top: `${bounds.top}px`,
				width: `${bounds.width}px`,
				height: `${bounds.height}px`,
				boxShadow: "0 8px 28px rgba(15,23,42,0.16)"
			});
		}
	}
	function reply(data) {
		if (!disposed) frame.contentWindow?.postMessage({
			...data,
			sessionId: target.run_id,
			bridgeId: target.mount_id
		}, url.origin);
	}
	async function receive(event) {
		const data = event.data;
		if (disposed || wb.conversations.currentId !== target.session_id || event.source !== frame.contentWindow || event.origin !== url.origin || data?.bridgeId !== target.mount_id || data.sessionId !== target.run_id) return;
		if (data.type === "lazymind.workflow.frame-status" && [
			"loading",
			"ready",
			"frontend-unavailable",
			"load-timeout"
		].includes(data.status)) {
			pageStatus = data.status;
			if (pageStatus === "ready") {
				reply({
					type: "lazymind.workflow.expansion",
					expanded
				});
				reply({
					type: "lazymind.workflow.collapse",
					collapsed
				});
			}
			return;
		}
		if (data.type === "lazymind.workflow.toggle-expand") {
			setExpanded(!expanded);
			reply({
				type: "lazymind.workflow.expansion",
				expanded
			});
			return;
		}
		if (data.type === "lazymind.workflow.toggle-collapse") {
			if (expanded) {
				setExpanded(false);
				reply({
					type: "lazymind.workflow.expansion",
					expanded
				});
			}
			collapsed = !collapsed;
			size();
			reply({
				type: "lazymind.workflow.collapse",
				collapsed
			});
			return;
		}
		if (data.type !== "lazymind.workflow.download" || typeof data.requestId !== "string" || !/^[a-zA-Z0-9-]{20,80}$/.test(data.requestId) || seen.has(data.requestId)) return;
		const respond = (ok, error) => reply({
			type: "lazymind.workflow.download.result",
			requestId: data.requestId,
			ok,
			error
		});
		if (seen.size >= 1e3) {
			respond(false, "请重新打开面板后下载。");
			return;
		}
		seen.add(data.requestId);
		let ownsSave = false;
		try {
			if (typeof wb.fileTransfer?.saveAs !== "function") throw Error("WorkBuddy 文件保存不可用。");
			if (data.action === "capabilities") {
				respond(true);
				return;
			}
			if (data.action !== "save" || !(data.blob instanceof Blob) || typeof data.filename !== "string" || !data.filename.trim() || data.filename.length > 240 || /[\/\\\u0000-\u001f]/.test(data.filename)) throw Error("无效的下载文件。");
			if (saving) throw Error("请先完成当前保存对话框。");
			ownsSave = saving = true;
			const base64 = await new Promise((resolve, reject) => {
				const reader = new FileReader();
				reader.onload = () => resolve(String(reader.result).split(",")[1]);
				reader.onerror = () => reject(Error("无法读取下载文件。"));
				reader.readAsDataURL(data.blob);
			});
			if (disposed || wb.conversations.currentId !== target.session_id) return;
			if ((await wb.fileTransfer.saveAs({
				defaultFileName: data.filename,
				contentBase64: base64
			}))?.success !== true) throw Error("保存失败或已取消。");
			respond(true);
		} catch (error) {
			respond(false, error instanceof Error ? error.message : "文件保存失败。");
		} finally {
			if (ownsSave) saving = false;
		}
	}
	function status() {
		return {
			status: !layoutReady ? "layout-unavailable" : panel.isConnected ? "mounted" : "detached",
			page_status: pageStatus,
			version,
			session_id: target.session_id,
			run_id: target.run_id,
			expanded,
			tail_blank_trimmed: tailExtent?.status().trimmed ?? 0
		};
	}
	const layoutObserver = typeof ResizeObserver === "function" ? new ResizeObserver(sync) : null;
	const structureObserver = new MutationObserver(() => {
		if (disposed || syncQueued || layoutReady && panel.isConnected && cachedDoc?.isConnected && cachedDoc.dataset.rootId === target.session_id && cachedDoc.parentElement === panel.parentElement && !panel.nextElementSibling) return;
		syncQueued = true;
		queueMicrotask(() => {
			syncQueued = false;
			if (!disposed) sync();
		});
	});
	const unsubscribe = wb.conversations.onCurrentChange(() => {
		if (wb.conversations.currentId !== target.session_id) dispose();
	});
	function dispose() {
		if (disposed) return;
		disposed = true;
		if (typeof unsubscribe === "function") unsubscribe();
		window.removeEventListener("message", receive);
		window.removeEventListener("resize", sync);
		layoutObserver?.disconnect();
		structureObserver.disconnect();
		tailFollow?.dispose();
		tailExtent?.dispose();
		panel.remove();
		if (host[key]?.dispose === dispose) delete host[key];
	}
	host[key] = {
		version,
		session: target.session_id,
		run: target.run_id,
		origin: url.origin,
		sync,
		dispose,
		status,
		tailFollow
	};
	window.addEventListener("message", receive);
	window.addEventListener("resize", sync);
	size();
	sync();
	return status();
}
function presentationExpression(target) {
	return `(${mountPresentation.toString()})(${JSON.stringify(target)}, ${installTailFollow.toString()}, ${installTailExtent.toString()}, ${workbuddyVersionIssue.toString()})`;
}
async function presentationLoop(cdp, request, bridgeURL, signal, report = () => {}) {
	let native_session_id, run_id;
	function publish(status, page_status) {
		report({
			status,
			page_status,
			checked_at: Date.now(),
			native_session_id,
			run_id
		});
	}
	while (!signal.aborted) {
		native_session_id = void 0;
		run_id = void 0;
		let stage = "host";
		let epoch, retryLayout = false;
		try {
			const current = await cdp.evaluate(`(${readPresentationSession.toString()})()`, signal);
			epoch = current?.epoch;
			const session = current?.session_id;
			native_session_id = session ?? void 0;
			if (session) {
				stage = "binding";
				const target = await request(`presentation?session_id=${encodeURIComponent(session)}`);
				signal.throwIfAborted();
				if (target.session_id !== session || typeof target.run_id !== "string") throw Error("Invalid presentation response");
				run_id = target.run_id || void 0;
				stage = "renderer";
				const result = await cdp.evaluate(presentationExpression({
					...target,
					epoch: current.epoch,
					frame_url: new URL("/v1/workbuddy/frame", bridgeURL).href,
					mount_id: crypto.randomUUID()
				}), signal);
				publish(result.status, result.page_status);
				retryLayout = result.status === "layout-unavailable" || result.status === "stale" || result.status === "page-not-ready";
			} else {
				await cdp.evaluate("window.__lazymindWorkflowTail?.dispose()", signal);
				publish(current ? "waiting-session" : "page-not-ready");
			}
		} catch (error) {
			const status = error?.status;
			if (status === 401 || status === 403) await cdp.evaluate("window.__lazymindWorkflowTail?.dispose()", signal).catch(() => {});
			if (!signal.aborted) publish(status === 401 || status === 403 ? "access-denied" : stage === "binding" ? "binding-unavailable" : stage === "host" ? "host-offline" : "renderer-unavailable");
			epoch = void 0;
		}
		if (signal.aborted) break;
		try {
			if (epoch && !retryLayout) await cdp.evaluate(`window.__lazymindWorkflowPresentationSession?.wait(${JSON.stringify(epoch)})`, signal);
			else await setTimeout$1(retryLayout ? 100 : 1e3, void 0, { signal });
		} catch {
			if (signal.aborted) break;
			try {
				await setTimeout$1(1e3, void 0, { signal });
			} catch {
				break;
			}
		}
	}
	await cdp.evaluate(cleanupPresentation, AbortSignal.timeout(2e3)).catch(() => {});
	publish("disabled");
}

//#endregion
//#region src/diagnostics.ts
function createDiagnostics(connectorId, sink = console.log) {
	return (event, fields) => {
		const data = {
			time: (/* @__PURE__ */ new Date()).toISOString(),
			component: "workbuddy",
			connector_id: connectorId,
			event
		};
		for (const key of [
			"action_id",
			"run_id",
			"native_session_id",
			"execution_id",
			"identity_request_id",
			"native_request_id",
			"tool_call_id",
			"kind",
			"status",
			"page_status"
		]) if (typeof fields[key] === "string") data[key] = fields[key].slice(0, 256);
		if (Number.isFinite(fields.duration_ms)) data.duration_ms = Math.max(0, Math.round(fields.duration_ms));
		try {
			sink(JSON.stringify(data));
		} catch {}
	};
}
function diagnosedTransport(bridge, log) {
	return {
		bind: (...args) => bridge.bind(...args),
		state: (...args) => bridge.state(...args),
		actions: (...args) => bridge.actions(...args),
		action: (...args) => bridge.action(...args),
		async claim(...args) {
			const started = performance.now();
			try {
				const result = await bridge.claim(...args), a = result.action;
				log("action.claim", {
					action_id: a.id,
					run_id: a.session_id,
					native_session_id: a.native_session_id,
					execution_id: a.execution_id,
					kind: a.kind,
					status: a.status,
					duration_ms: performance.now() - started
				});
				return result;
			} catch (error) {
				log("action.claim", {
					action_id: args[0],
					status: "unconfirmed",
					duration_ms: performance.now() - started
				});
				throw error;
			}
		},
		async settle(...args) {
			const started = performance.now();
			try {
				await bridge.settle(...args);
				log("action.settle", {
					action_id: args[0],
					status: args[3],
					duration_ms: performance.now() - started
				});
			} catch (error) {
				log("action.settle", {
					action_id: args[0],
					status: "unconfirmed",
					duration_ms: performance.now() - started
				});
				throw error;
			}
		}
	};
}

//#endregion
//#region src/main.ts
async function main() {
	const { values } = parseArgs({ options: {
		"pairing-file": { type: "string" },
		"bridge-url": {
			type: "string",
			default: "http://127.0.0.1:19091"
		},
		"cdp-port": {
			type: "string",
			default: "9333"
		},
		"parent-pid": { type: "string" },
		"cleanup-only": {
			type: "boolean",
			default: false
		}
	} });
	const path = values["pairing-file"];
	if (!path) throw new Error("pairing-file required");
	const info = await stat(path);
	if (!info.isFile() || process.platform !== "win32" && (info.mode & 63) !== 0) throw new Error("Private pairing file required");
	const pairing = JSON.parse(await readFile(path, "utf8"));
	if (pairing.provider !== "workbuddy" || !pairing.enabled && !values["cleanup-only"] || !/^host-[a-f0-9]{32}$/.test(pairing.connector_id) || !/^[a-f0-9]{64}$/.test(pairing.token)) throw new Error("Invalid WorkBuddy pairing");
	if (values["cleanup-only"]) {
		if (pairing.enabled) throw new Error("Disable the pairing before cleaning presentation");
		const deadline = Date.now() + 5e3;
		let release;
		while (!release) try {
			release = await dispatcherLock(path, randomUUID());
		} catch (error) {
			if (!(error instanceof DispatcherBusy) || Date.now() >= deadline) throw error;
			await setTimeout$1(100);
		}
		try {
			await new Cdp(Number(values["cdp-port"])).evaluate("window.__lazymindWorkflowTail?.dispose(); window.__lazymindWorkflowPresentationSession?.dispose()", AbortSignal.timeout(2e3));
		} finally {
			await release();
		}
		return;
	}
	const bridge = new HostBridge(values["bridge-url"], pairing);
	const lifetime = new AbortController();
	const signal = lifetime.signal;
	const stop = () => lifetime.abort();
	process.once("SIGINT", stop);
	process.once("SIGTERM", stop);
	const parent = values["parent-pid"] ? Number(values["parent-pid"]) : void 0;
	if (parent !== void 0 && (!Number.isSafeInteger(parent) || parent < 1)) throw new Error("Invalid parent PID");
	const cdp = new Cdp(Number(values["cdp-port"]));
	const log = createDiagnostics(pairing.connector_id);
	const adapter = new WorkBuddyAdapter(cdp, log);
	const transport = diagnosedTransport(bridge, log);
	const instance = randomUUID();
	const unlock = await dispatcherLock(path, instance);
	const timer = setInterval(async () => {
		try {
			if (parent) process.kill(parent, 0);
			const current = JSON.parse(await readFile(path, "utf8"));
			if (!current.enabled || current.token !== pairing.token) stop();
		} catch {
			stop();
		}
	}, 1e3);
	const statusPath = path + ".workbuddy-status.json";
	const temporaryStatus = statusPath + "." + instance;
	let presentation = {
		status: "starting",
		checked_at: Date.now()
	};
	async function heartbeat() {
		while (!signal.aborted) {
			let version = "", ready = false, issue = "host_unavailable";
			try {
				const probe = await adapter.probe(signal);
				version = probe.version;
				issue = probe.issue || "";
				ready = !issue;
			} catch {}
			try {
				await writeFile(temporaryStatus, JSON.stringify({
					connector_id: pairing.connector_id,
					checked_at: Date.now(),
					ready,
					version,
					issue,
					cancellation: adapter.cancellation,
					bridge_url: values["bridge-url"],
					presentation
				}), { mode: 384 });
				await rename(temporaryStatus, statusPath);
			} catch {}
			try {
				await setTimeout$1(2e3, void 0, { signal });
			} catch {
				break;
			}
		}
	}
	const request = async (route, body) => {
		const response = await fetch(values["bridge-url"] + "/v1/workbuddy/" + route, {
			method: body === void 0 ? "GET" : "POST",
			redirect: "error",
			signal: AbortSignal.any([signal, AbortSignal.timeout(15e3)]),
			headers: {
				Authorization: `Bearer ${pairing.token}`,
				"X-LazyMind-Connector-Id": pairing.connector_id,
				...body === void 0 ? {} : { "content-type": "application/json" }
			},
			...body === void 0 ? {} : { body: JSON.stringify(body) }
		});
		if (!response.ok) throw Object.assign(/* @__PURE__ */ new Error("WorkBuddy bridge unavailable"), { status: response.status });
		return response.json();
	};
	async function identities() {
		while (!signal.aborted) {
			try {
				const page = await request("identity-requests");
				for (const item of page.requests) try {
					const started = performance.now();
					const identity = await adapter.identify(item.arguments, signal);
					await request("identity-receipts", {
						id: item.id,
						...identity
					});
					log("identity.resolved", {
						identity_request_id: item.id,
						native_session_id: identity.sessionId,
						native_request_id: identity.requestId,
						tool_call_id: identity.toolCallId,
						status: "resolved",
						duration_ms: performance.now() - started
					});
				} catch {}
			} catch {}
			try {
				await setTimeout$1(250, void 0, { signal });
			} catch {
				break;
			}
		}
	}
	try {
		console.log("WorkBuddy connector started; session wake and interruption enabled");
		let lastPresentation = "";
		await Promise.all([
			createDispatcher(adapter, adapter.control(signal), transport, instance, signal).poll(),
			identities(),
			heartbeat(),
			presentationLoop(cdp, request, values["bridge-url"], signal, (value) => {
				presentation = value;
				const fields = {
					status: value.status,
					page_status: value.page_status,
					run_id: value.run_id,
					native_session_id: value.native_session_id
				};
				const key = JSON.stringify(fields);
				if (key !== lastPresentation) {
					lastPresentation = key;
					log("presentation", fields);
				}
			})
		]);
	} finally {
		stop();
		clearInterval(timer);
		await unlock();
		await unlink(temporaryStatus).catch(() => {});
		process.removeListener("SIGINT", stop);
		process.removeListener("SIGTERM", stop);
	}
}
main().catch((error) => {
	console.error(String(error));
	process.exitCode = 1;
});

//#endregion
export {  };