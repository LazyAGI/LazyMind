import { mkdir, open, readFile, rename, unlink, writeFile } from "node:fs/promises";
import { parseArgs } from "node:util";
import { createHash, randomUUID } from "node:crypto";
import { dirname, join } from "node:path";
import { setTimeout as setTimeout$1 } from "node:timers/promises";

//#region ../workflow-agent-core/src/adapter.ts
/** Use only when the host definitely did not receive the input. */
var AdmissionRejected = class extends Error {};

//#endregion
//#region ../workflow-agent-core/src/transport.ts
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
//#region ../workflow-agent-core/src/dispatcher.ts
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
//#region ../workflow-agent-core/node/runtime-lock.ts
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
//#region src/ownership.ts
/** Serialized into the renderer as well: keep all runtime helpers inside this function. */
function workflowTurnEvidence(turn) {
	const object = (value) => value && typeof value === "object" && !Array.isArray(value) ? value : void 0;
	const inputs = [];
	let run;
	for (const value of turn.items ?? []) {
		const item = object(value);
		if (!item) continue;
		if ([
			"userMessage",
			"steeringUserMessage",
			"steered"
		].includes(item.type)) {
			inputs.push(item);
			run = void 0;
			continue;
		}
		if (item.type !== "mcpToolCall" || !["lazymind", "lazymind-workflow"].includes(String(item.server)) || ![
			"workflow.start",
			"workflow.step.begin",
			"workflow.step.claim",
			"workflow.step.resume",
			"workflow.step.complete"
		].includes(String(item.tool)) || item.status !== "completed" || item.error) continue;
		const result = object(item.result), fields = object(result?.structuredContent);
		if (!fields || result?.isError === true) continue;
		const state = object(fields.state), execution = object(fields.execution);
		const id = fields.session_id ?? state?.session_id;
		if (typeof id !== "string" || !id) continue;
		if (item.tool !== "workflow.start" && object(item.arguments)?.session_id !== id) continue;
		if ([
			"workflow.step.begin",
			"workflow.step.claim",
			"workflow.step.resume"
		].includes(String(item.tool)) && typeof execution?.execution_id !== "string") continue;
		run = id;
	}
	return {
		runId: run,
		inputs
	};
}

//#endregion
//#region src/adapter.ts
var CodexAdapter = class {
	cancellation = "session";
	claims = /* @__PURE__ */ new Map();
	host = {
		cacheClaim: (claim) => {
			this.claims.set(claim.action.id, claim);
		},
		cancelSession: (thread, claim) => this.cancelOwned(thread, claim)
	};
	constructor(native, receipts, bridge, signal, recovery) {
		this.native = native;
		this.receipts = receipts;
		this.bridge = bridge;
		this.signal = signal;
		this.recovery = recovery;
	}
	id(thread) {
		return thread;
	}
	warn(message) {
		console.warn(message);
	}
	async resolve(thread) {
		return /^[a-f0-9-]{36}$/i.test(thread) ? { agent: thread } : { error: "A Codex thread UUID is required" };
	}
	async ready(thread, signal) {
		try {
			const state = await this.native.read(thread, signal);
			this.recovery?.available();
			return state.ready !== false && !state.active;
		} catch {
			if (!signal.aborted) await this.recovery?.unavailable(thread);
			return false;
		}
	}
	validate(thread, claim, kind) {
		const { action, control } = claim;
		if (action.kind !== kind || action.status !== "dispatching" || action.consumed_at || action.native_session_id !== thread || control.session_id !== action.session_id || !control.binding?.bound || control.binding.driver_session_id !== thread || control.binding.provider !== "codex") throw new AdmissionRejected("Notification does not match the bound Codex chat");
		if (kind === "cancel" && control.continuation !== "stopped") throw new AdmissionRejected("Workflow is no longer stopped");
	}
	async prompt(thread, input, signal) {
		const claim = this.claims.get(input.actionId);
		this.claims.delete(input.actionId);
		if (!claim) throw new AdmissionRejected("Workflow notification has not been claimed");
		this.validate(thread, claim, "continue");
		if (await this.receipts.read(input.actionId)) throw new Error("Message was already attempted; do not resend");
		const before = await this.native.read(thread, signal);
		if (before.active) throw new AdmissionRejected("Codex chat became busy; no message was sent");
		signal.throwIfAborted();
		const receipt = {
			actionId: input.actionId,
			runId: claim.action.session_id,
			threadId: thread,
			kind: "continue",
			clientId: randomUUID(),
			status: "sending",
			turnId: void 0
		};
		await this.receipts.write(receipt);
		try {
			const message = `[LazyMind action_id=${input.actionId}]\n${input.message}`;
			let result = await this.native.start(thread, receipt.clientId, message, signal);
			for (let retries = 0; result?.rejected && result.retryable === true && retries < 2; retries++) {
				receipt.status = "rejected";
				await this.receipts.write(receipt);
				await setTimeout$1(250, void 0, { signal });
				this.validate(thread, await this.bridge.action(input.actionId, signal), "continue");
				signal.throwIfAborted();
				receipt.status = "sending";
				await this.receipts.write(receipt);
				result = await this.native.start(thread, receipt.clientId, message, signal);
			}
			if (result?.rejected) {
				receipt.status = "rejected";
				throw new AdmissionRejected(result.rejected);
			}
			if (!result?.accepted || !result.turnId || before.turns.some((turn) => turn.id === result.turnId)) throw new Error("Native message receipt is uncertain");
			receipt.turnId = result.turnId;
			receipt.status = "accepted";
			await this.receipts.write(receipt);
			return 0;
		} catch (error) {
			if (receipt.status !== "rejected") receipt.status = "unknown";
			await this.receipts.write(receipt);
			throw error;
		}
	}
	cancel() {
		throw new AdmissionRejected("Stop requires a Workflow-owned turn");
	}
	async owns(evidence, turnId, run, thread) {
		const { runId: owner, inputs } = evidence;
		if (owner) return owner === run;
		if (inputs.length !== 1 || inputs[0].type !== "userMessage") return false;
		const text = inputs[0].content?.find((part) => part.type === "text")?.text;
		const id = typeof text === "string" ? /^\[LazyMind action_id=([^\]\n]+)\]/.exec(text)?.[1] : void 0;
		if (!id) return false;
		const receipt = await this.receipts.read(id);
		return receipt?.kind === "continue" && receipt.runId === run && receipt.threadId === thread && receipt.clientId === inputs[0].clientId && (!receipt.turnId || receipt.turnId === turnId);
	}
	async cancelOwned(thread, claim) {
		this.claims.delete(claim.action.id);
		this.validate(thread, claim, "cancel");
		if (await this.receipts.read(claim.action.id)) throw new Error("Stop was already attempted; do not replay it");
		const state = await this.native.read(thread, this.signal);
		const active = state.turns.filter((turn) => turn.status === "inProgress");
		if (!active.length && !state.active) return;
		const target = active[0];
		if (active.length !== 1 || !target.id) throw new AdmissionRejected("Current Codex work is not the target of this Stop");
		const evidence = workflowTurnEvidence(target), expectedOwnership = JSON.stringify(evidence);
		const stoppedAt = Date.parse(claim.action.created_at ?? "");
		if (!await this.owns(evidence, target.id, claim.action.session_id, thread) || !Number.isFinite(stoppedAt) || !Number.isFinite(target.startedAt) || target.startedAt > stoppedAt) throw new AdmissionRejected("Current Codex work is not the target of this Stop");
		this.validate(thread, await this.bridge.action(claim.action.id, this.signal), "cancel");
		await this.receipts.write({
			actionId: claim.action.id,
			runId: claim.action.session_id,
			threadId: thread,
			kind: "cancel",
			turnId: target.id,
			status: "sending"
		});
		const result = await this.native.interrupt(thread, target.id, expectedOwnership, this.signal);
		if (result?.rejected) throw new AdmissionRejected(result.rejected);
		if (result.interruptedTurnId !== target.id) throw new Error("The target turn interruption was not confirmed");
		await this.receipts.write({
			actionId: claim.action.id,
			runId: claim.action.session_id,
			threadId: thread,
			kind: "cancel",
			turnId: target.id,
			status: "accepted"
		});
	}
};

//#endregion
//#region ../workflow-agent-core/node/cdp.ts
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
//#region src/windows.ts
function isCodexChatTarget(target) {
	if (target.type !== "page") return false;
	try {
		const url = new URL(target.url);
		if (url.protocol !== "app:" || url.host !== "-") return false;
		if (url.pathname === "/index.html") {
			const route = url.searchParams.get("initialRoute");
			return route === null || /^\/local\/[a-f0-9-]{36}$/i.test(route);
		}
		return url.pathname === "/detached-window.html" && (url.searchParams.get("initialRoute") ?? "/detached-window") === "/detached-window";
	} catch {
		return false;
	}
}
function selectCodexTarget(targets, id) {
	const pages = targets.filter((target) => isCodexChatTarget(target) && (!id || target.id === id));
	if (pages.length !== 1) throw Error(id ? "Codex window is no longer available" : "Expected one Codex window; select a specific window");
	return pages[0];
}
async function codexTargets(port, signal) {
	const response = await fetch(`http://127.0.0.1:${port}/json/list`, {
		signal: AbortSignal.any([signal, AbortSignal.timeout(3e3)]),
		redirect: "error"
	});
	if (!response.ok) throw Error("Codex CDP discovery failed");
	return (await response.json()).filter((target) => isCodexChatTarget(target) && typeof target.id === "string");
}
function windowCdp(port, id) {
	return new CdpClient(port, (targets) => selectCodexTarget(targets, id));
}

//#endregion
//#region ../workflow-agent-core/renderer/tail-follow.ts
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
//#region src/presentation-view.ts
/** Presentation identity comes from the committed main-chat view, never binds a run. */
function readCodexView() {
	const scrollers = Array.from(document.querySelectorAll("[data-app-action-timeline-scroll]")).filter((element) => element.getBoundingClientRect().width > 0);
	if (scrollers.length !== 1) return null;
	const scroller = scrollers[0];
	const fiberKey = Object.keys(scroller).find((key) => key.startsWith("__reactFiber$"));
	let fiber = fiberKey ? scroller[fiberKey] : null;
	let root = fiber;
	while (root?.return) root = root.return;
	if (root?.stateNode?.current && root.stateNode.current !== root) fiber = fiber?.alternate;
	let session;
	for (let depth = 0; fiber && depth < 60; depth++, fiber = fiber.return) {
		const props = fiber.memoizedProps;
		if (typeof props?.conversationId === "string" && props.hostId === "local") {
			session = props.conversationId;
			break;
		}
	}
	if (!session || !/^[a-f0-9-]{36}$/i.test(session)) return null;
	const transcript = scroller.querySelector("[data-thread-user-message-navigation-content]");
	const content = transcript?.parentElement;
	if (!transcript || !content || content.parentElement !== scroller) return null;
	const spacer = Array.from(content.children).find((element) => element !== transcript && element.classList.contains("sticky")) ?? null;
	return {
		session,
		scroller,
		content,
		transcript,
		spacer
	};
}
/** Serialized into the renderer together with readCodexView. */
function readPresentationSession(readView = readCodexView) {
	const host = window;
	const key = "__lazymindCodexPresentationSession";
	const version = "codex-view-v1";
	if (host[key]?.version !== version) host[key]?.dispose();
	if (!host[key]) {
		let view = readView(), epoch = crypto.randomUUID(), queued = false;
		const listeners = /* @__PURE__ */ new Set(), waiters = /* @__PURE__ */ new Set();
		const check = () => {
			const next = readView();
			if (view?.session === next?.session && view?.scroller === next?.scroller) return;
			view = next;
			epoch = crypto.randomUUID();
			for (const listener of listeners) listener();
			for (const done of [...waiters]) done();
		};
		const observer = new MutationObserver(() => {
			if (queued) return;
			queued = true;
			queueMicrotask(() => {
				queued = false;
				check();
			});
		});
		observer.observe(document.body, {
			childList: true,
			subtree: true
		});
		const gate = {
			version,
			read: () => {
				check();
				return {
					session_id: view?.session ?? null,
					epoch
				};
			},
			subscribe: (listener) => {
				listeners.add(listener);
				return () => listeners.delete(listener);
			},
			wait: (expected) => {
				if (gate.read().epoch !== expected) return Promise.resolve();
				return new Promise((resolve) => {
					const done = () => {
						clearTimeout(timer);
						waiters.delete(done);
						resolve();
					};
					const timer = setTimeout(done, 5e3);
					waiters.add(done);
				});
			},
			dispose: () => {
				observer.disconnect();
				listeners.clear();
				for (const done of [...waiters]) done();
				if (host[key] === gate) delete host[key];
			}
		};
		host[key] = gate;
	}
	return host[key].read();
}

//#endregion
//#region src/presentation-surface.ts
/** Locate the desktop's existing browser service by capabilities, not minified names. */
async function prepareCodexBrowser() {
	const host = window;
	if (typeof host.__lazymindCodexBrowser?.pages?.getWebview === "function") return;
	const link = Array.from(document.querySelectorAll("link[href]")).find((element) => /^app:\/\/-\/assets\/app-initial-[^/]+\.js$/.test(element.href));
	if (!link) throw Error("Codex browser module is unavailable");
	const exports = await import(
		/* @vite-ignore */
		link.href
);
	const manager = Object.values(exports).find((value) => value && typeof value === "object" && typeof value.pages?.getWebview === "function" && typeof value.pages?.disposeHost === "function" && typeof value.getSnapshot === "function");
	if (!manager) throw Error("Codex native browser interface is unavailable");
	host.__lazymindCodexBrowser = manager;
}
/** Native browser guest: its own process and CSP, using the desktop's registration. */
function createCodexSurface(target, url, receive, changed) {
	const browser = window.__lazymindCodexBrowser;
	if (!browser?.pages?.getWebview) throw Error("Codex native browser interface is unavailable");
	const tab = target.mount_id;
	const native = browser.pages.getWebview(target.session_id, tab, "", { hostKind: "right-panel" });
	const guest = native.webview;
	const element = document.createElement("div");
	element.dataset.lazymindWorkflowSurface = "";
	element.style.cssText = "width:100%;height:clamp(360px,60vh,640px)";
	const ref = { current: null };
	let disposed = false, loading = false, loaded = false, draining = false, expanded = false, lastGeometry = "";
	url.hash = new URLSearchParams({
		run: target.run_id,
		mount: target.mount_id,
		native: "1"
	}).toString();
	function valid() {
		if (disposed) return false;
		try {
			return guest.getURL() === url.href;
		} catch {
			return false;
		}
	}
	function reply(data) {
		if (!valid()) return;
		guest.executeJavaScript(`window.__lazymindWorkflowFrame?.receive(${JSON.stringify({
			...data,
			sessionId: target.run_id,
			bridgeId: target.mount_id
		})})`).catch(() => {});
	}
	async function navigate() {
		if (disposed || loaded || loading) return;
		loading = true;
		try {
			await guest.loadURL(url.href);
			loaded = true;
		} catch {} finally {
			loading = false;
		}
	}
	function ready() {
		if (!valid()) navigate();
	}
	guest.addEventListener("dom-ready", ready);
	let registered = native.isRegistered;
	const registrationTimer = typeof native.listenForDidRegister === "function" ? void 0 : setInterval(() => {
		if (disposed || registered === native.isRegistered) return;
		registered = native.isRegistered;
		changed();
	}, 100);
	const unregister = typeof native.listenForDidRegister === "function" ? native.listenForDidRegister(changed) : () => clearInterval(registrationTimer);
	function sync(bounds, clip, visible) {
		if (disposed) return;
		const state = {
			bounds,
			isVisible: visible,
			clipBounds: clip,
			scale: 1,
			windowZoom: 1,
			shouldPaint: true,
			mountGeneration: 1
		};
		native.sync(state, ref);
		native.container.style.borderRadius = "10px";
		native.container.style.backgroundColor = "transparent";
		guest.style.borderRadius = "inherit";
		guest.style.clipPath = "inset(0 round 10px)";
		guest.style.backgroundColor = "transparent";
		if (native.isRegistered) {
			const geometry = JSON.stringify({
				bounds,
				visible
			});
			if (lastGeometry !== geometry) {
				lastGeometry = geometry;
				native.browserHostService.syncView({
					browserTabId: tab,
					conversationId: target.session_id,
					hostKind: "right-panel",
					mountGeneration: 1,
					bounds: visible ? bounds : null,
					visible,
					presented: visible,
					viewportScale: 1
				}).catch(() => {});
			}
			if (!loaded) navigate();
		}
		if (expanded && !native.container.matches(":popover-open")) {
			native.container.setAttribute("popover", "manual");
			native.container.style.margin = "0";
			native.container.style.padding = "0";
			native.container.style.border = "0";
			try {
				native.container.showPopover();
			} catch {}
		}
	}
	function setExpanded(next) {
		expanded = next;
		if (!next && native.container.hasAttribute("popover")) {
			try {
				native.container.hidePopover();
			} catch {}
			native.container.removeAttribute("popover");
		}
	}
	const timer = setInterval(async () => {
		if (disposed || draining || !valid()) return;
		draining = true;
		try {
			const events = await guest.executeJavaScript("window.__lazymindWorkflowFrame?.drain() ?? []");
			if (disposed) return;
			for (const data of events) if (data?.sessionId === target.run_id && data.bridgeId === target.mount_id) receive(data);
		} catch {} finally {
			draining = false;
		}
	}, 150);
	async function probeStatus() {
		if (!valid()) return void 0;
		const status = await guest.executeJavaScript("window.__lazymindWorkflowFrame?.status()");
		if (!valid() || status?.type !== "lazymind.workflow.frame-status" || status.sessionId !== target.run_id || status.bridgeId !== target.mount_id) return void 0;
		return status;
	}
	return {
		element,
		reply,
		sync,
		setExpanded,
		probeStatus,
		dispose: () => {
			if (disposed) return;
			disposed = true;
			clearInterval(timer);
			unregister();
			guest.removeEventListener("dom-ready", ready);
			setExpanded(false);
			browser.pages.disposeHost(target.session_id, tab, "closed");
			element.remove();
		}
	};
}

//#endregion
//#region src/presentation.ts
/** Serialized with its two helpers; mutates presentation only. */
function mountPresentation(target, readView = readCodexView, followTail = installTailFollow, makeSurface = createCodexSurface) {
	const host = window, key = "__lazymindCodexWorkflowTail", version = "codex-tail-v3-ready";
	const gate = host.__lazymindCodexPresentationSession;
	if (!gate || gate.read().epoch !== target.epoch || gate.read().session_id !== target.session_id) return { status: "stale" };
	const previous = host[key];
	if (!target.run_id) {
		previous?.dispose();
		return { status: "unbound" };
	}
	const url = new URL(target.frame_url);
	if (url.protocol !== "http:" || !["localhost", "127.0.0.1"].includes(url.hostname) || url.username || url.password || url.pathname !== "/v1/codex/frame" || url.search || url.hash || !/^[a-zA-Z0-9_-]{1,256}$/.test(target.run_id) || !/^[a-zA-Z0-9-]{20,80}$/.test(target.mount_id)) throw Error("Invalid presentation target");
	if (previous?.version === version && previous.session === target.session_id && previous.run === target.run_id && previous.origin === url.origin) {
		previous.sync();
		return previous.status();
	}
	previous?.dispose();
	const locate = () => {
		const view = readView();
		return view?.session === target.session_id ? view : null;
	};
	const initial = locate();
	if (!initial) return { status: "layout-unavailable" };
	let scroller = initial.scroller;
	const panel = document.createElement("section");
	panel.dataset.lazymindWorkflowTail = target.session_id;
	panel.setAttribute("aria-label", "LazyMind Workflow");
	panel.style.cssText = "box-sizing:border-box;position:relative;flex:none;margin:16px auto 24px;width:calc(100% - 32px);max-width:var(--thread-body-max-width,var(--thread-content-max-width,760px));overflow:hidden;border:0;border-radius:12px;";
	const surface = makeSurface(target, url, receive, () => sync());
	panel.append(surface.element);
	let disposed = false, expanded = false, collapsed = false, layoutReady = false, pageStatus = "loading", queued = false;
	let follower, followedContent;
	let observedShell;
	function current() {
		return gate.read().session_id === target.session_id;
	}
	function size() {
		surface.element.style.height = collapsed ? "64px" : "clamp(360px, 60vh, 640px)";
	}
	function bounds() {
		const shell = locate()?.scroller.parentElement;
		if (!shell) return null;
		const r = shell.getBoundingClientRect(), left = Math.max(0, r.left) + 12, top = Math.max(0, r.top) + 12;
		const right = Math.min(window.innerWidth, r.right) - 12, bottom = Math.min(window.innerHeight, r.bottom) - 12;
		return right > left && bottom > top ? {
			left,
			top,
			width: right - left,
			height: bottom - top
		} : null;
	}
	function reply(data) {
		if (!disposed && current()) surface.reply(data);
	}
	function setExpanded(next) {
		if (expanded === next || next && !bounds()) return;
		expanded = next;
		if (next) {
			collapsed = false;
			reply({
				type: "lazymind.workflow.collapse",
				collapsed
			});
		}
		surface.setExpanded(next);
		size();
		sync();
	}
	function sync() {
		if (disposed) return;
		if (!current()) {
			dispose();
			return;
		}
		const view = locate();
		if (!view) {
			layoutReady = false;
			panel.hidden = true;
			follower?.dispose();
			surface.sync({
				x: 0,
				y: 0,
				width: 0,
				height: 0
			}, {
				x: 0,
				y: 0,
				width: 0,
				height: 0
			}, false);
			if (expanded) {
				setExpanded(false);
				reply({
					type: "lazymind.workflow.expansion",
					expanded
				});
			}
			return;
		}
		if (scroller !== view.scroller) {
			scroller.removeEventListener("scroll", sync);
			scroller = view.scroller;
			scroller.addEventListener("scroll", sync, { passive: true });
			follower?.dispose();
		}
		layoutReady = true;
		panel.hidden = false;
		const following = follower && followedContent === view.content ? follower.status().following : view.scroller.scrollHeight - view.scroller.clientHeight - view.scroller.scrollTop <= 24;
		if (panel.parentElement !== view.content || panel.nextElementSibling !== view.spacer) {
			follower?.dispose();
			const move = view.content.moveBefore;
			if (panel.isConnected && typeof move === "function") move.call(view.content, panel, view.spacer);
			else view.content.insertBefore(panel, view.spacer);
		}
		if (!follower || follower.status().disposed || followedContent !== view.content) {
			follower?.dispose();
			followedContent = view.content;
			follower = followTail(view.scroller, view.content, panel, following, "[data-thread-scroll-footer] button[class~=\"group/scroll-to-bottom\"]");
		}
		const shell = view.scroller.parentElement;
		if (observedShell !== shell) {
			observedShell = shell;
			resize?.disconnect();
			structure.disconnect();
			resize?.observe(shell);
			resize?.observe(view.transcript);
			resize?.observe(panel);
			structure.observe(shell, {
				childList: true,
				subtree: true
			});
		}
		const placeholder = surface.element.getBoundingClientRect();
		const viewport = view.scroller.getBoundingClientRect();
		const footerHeight = view.scroller.querySelector("[data-thread-scroll-footer]")?.getBoundingClientRect().height ?? 0;
		const rect = expanded ? bounds() : {
			left: placeholder.left,
			top: placeholder.top,
			width: placeholder.width,
			height: placeholder.height
		};
		const clip = expanded ? {
			x: rect.left,
			y: rect.top,
			width: rect.width,
			height: rect.height
		} : {
			x: Math.max(0, viewport.left),
			y: Math.max(0, viewport.top),
			width: viewport.width,
			height: Math.max(0, Math.min(window.innerHeight, viewport.bottom - footerHeight) - Math.max(0, viewport.top))
		};
		const visible = rect.width > 0 && rect.height > 0 && rect.top + rect.height > clip.y && rect.top < clip.y + clip.height;
		surface.sync({
			x: rect.left,
			y: rect.top,
			width: rect.width,
			height: rect.height
		}, clip, visible);
	}
	function receive(data) {
		if (disposed || !current() || data?.sessionId !== target.run_id || data.bridgeId !== target.mount_id) return;
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
		} else if (data.type === "lazymind.workflow.toggle-expand") {
			setExpanded(!expanded);
			reply({
				type: "lazymind.workflow.expansion",
				expanded
			});
		} else if (data.type === "lazymind.workflow.toggle-collapse") {
			if (expanded) {
				setExpanded(false);
				reply({
					type: "lazymind.workflow.expansion",
					expanded
				});
			}
			collapsed = !collapsed;
			size();
			sync();
			reply({
				type: "lazymind.workflow.collapse",
				collapsed
			});
		}
	}
	function status() {
		return {
			status: !layoutReady ? "layout-unavailable" : panel.isConnected ? "mounted" : "detached",
			page_status: pageStatus,
			session_id: target.session_id,
			run_id: target.run_id,
			expanded,
			collapsed
		};
	}
	const resize = typeof ResizeObserver === "function" ? new ResizeObserver(sync) : null;
	const structure = new MutationObserver(() => {
		if (queued || disposed) return;
		queued = true;
		queueMicrotask(() => {
			queued = false;
			sync();
		});
	});
	const unsubscribe = gate.subscribe(() => {
		if (!current()) dispose();
	});
	function dispose() {
		if (disposed) return;
		disposed = true;
		unsubscribe();
		resize?.disconnect();
		structure.disconnect();
		follower?.dispose();
		window.removeEventListener("resize", sync);
		scroller.removeEventListener("scroll", sync);
		surface.dispose();
		panel.remove();
		if (host[key]?.dispose === dispose) delete host[key];
	}
	async function probeStatus() {
		if (disposed || !current()) return void 0;
		const latest = await surface.probeStatus();
		if (disposed || !current()) return void 0;
		if (latest) receive(latest);
		sync();
		return status();
	}
	host[key] = {
		version,
		session: target.session_id,
		run: target.run_id,
		origin: url.origin,
		sync,
		status,
		probeStatus,
		dispose
	};
	window.addEventListener("resize", sync);
	scroller.addEventListener("scroll", sync, { passive: true });
	size();
	sync();
	return status();
}
function sessionExpression() {
	return `(${readPresentationSession.toString()})(${readCodexView.toString()})`;
}
function presentationExpression(target) {
	return `(async()=>{await (${prepareCodexBrowser.toString()})();return (${mountPresentation.toString()})(${JSON.stringify(target)},${readCodexView.toString()},${installTailFollow.toString()},${createCodexSurface.toString()})})()`;
}
const cleanupPresentation = "window.__lazymindCodexWorkflowTail?.dispose(); window.__lazymindCodexPresentationSession?.dispose()";
async function presentationWindowLoop(cdp, request, bridgeURL, signal, report) {
	while (!signal.aborted) {
		let native_session_id, run_id, epoch, retryLayout = false;
		try {
			const view = await cdp.evaluate(sessionExpression(), signal);
			epoch = view.epoch;
			native_session_id = view.session_id ?? void 0;
			if (!native_session_id) {
				await cdp.evaluate("window.__lazymindCodexWorkflowTail?.dispose()", signal);
				report({
					status: "waiting-session",
					checked_at: Date.now()
				});
			} else {
				const target = await request(native_session_id);
				if (target.session_id !== native_session_id || typeof target.run_id !== "string") throw Error("Invalid presentation response");
				run_id = target.run_id || void 0;
				const mounted = await cdp.evaluate(presentationExpression({
					...target,
					epoch,
					frame_url: new URL("/v1/codex/frame", bridgeURL).href,
					mount_id: crypto.randomUUID()
				}), signal);
				retryLayout = ["layout-unavailable", "stale"].includes(mounted.status);
				report({
					...mounted,
					checked_at: Date.now(),
					native_session_id,
					run_id
				});
			}
		} catch (error) {
			const denied = [401, 403].includes(error?.status);
			if (denied) await cdp.evaluate("window.__lazymindCodexWorkflowTail?.dispose()", signal).catch(() => {});
			report({
				status: denied ? "access-denied" : "unavailable",
				checked_at: Date.now(),
				native_session_id,
				run_id
			});
			epoch = void 0;
		}
		if (signal.aborted) break;
		try {
			if (epoch && !retryLayout) await cdp.evaluate(`window.__lazymindCodexPresentationSession?.wait(${JSON.stringify(epoch)})`, signal);
			else await setTimeout$1(retryLayout ? 100 : 1e3, void 0, { signal });
		} catch {
			if (!signal.aborted) await setTimeout$1(1e3, void 0, { signal }).catch(() => {});
		}
	}
	await cdp.evaluate(cleanupPresentation, AbortSignal.timeout(2e3)).catch(() => {});
	report({
		status: "disabled",
		checked_at: Date.now()
	});
}
async function cleanupCodexPresentations(port, signal) {
	const windows = await codexTargets(port, signal);
	await Promise.allSettled(windows.map((window$1) => windowCdp(port, window$1.id).evaluate(cleanupPresentation, signal)));
}
/** One delivery worker; a separate presentation loop for every chat window. */
async function presentationLoop(port, request, bridgeURL, signal, report) {
	const entries = /* @__PURE__ */ new Map();
	function publish() {
		const windows = [...entries.values()].map((entry) => entry.status);
		report({
			status: windows.find((window$1) => window$1.status === "mounted") ? "mounted" : windows[0]?.status ?? "waiting-window",
			checked_at: Date.now(),
			window_count: windows.length,
			windows
		});
	}
	try {
		while (!signal.aborted) {
			try {
				const targets = await codexTargets(port, signal), ids = new Set(targets.map((target) => target.id));
				for (const [id, entry] of entries) if (!ids.has(id)) {
					entry.stop.abort();
					entries.delete(id);
				}
				for (const target of targets) {
					if (entries.has(target.id)) continue;
					const stop = new AbortController(), entry = {
						stop,
						task: Promise.resolve(),
						status: {
							status: "starting",
							target_id: target.id,
							checked_at: Date.now()
						}
					};
					entries.set(target.id, entry);
					entry.task = presentationWindowLoop(windowCdp(port, target.id), request, bridgeURL, AbortSignal.any([signal, stop.signal]), (status) => {
						entry.status = {
							...status,
							target_id: target.id
						};
						if (entries.get(target.id) === entry) publish();
					});
				}
				publish();
			} catch {
				if (!signal.aborted) report({
					status: "host-offline",
					checked_at: Date.now(),
					window_count: entries.size,
					windows: [...entries.values()].map((entry) => entry.status)
				});
			}
			try {
				await setTimeout$1(1e3, void 0, { signal });
			} catch {
				break;
			}
		}
	} finally {
		for (const entry of entries.values()) entry.stop.abort();
		await Promise.allSettled([...entries.values()].map((entry) => entry.task));
		report({
			status: "disabled",
			checked_at: Date.now(),
			window_count: 0,
			windows: []
		});
	}
}

//#endregion
//#region src/native.ts
/** Runs inside the existing desktop renderer, never in a new App Server. */
async function codexOperation(input, readEvidence = workflowTurnEvidence) {
	const host = globalThis;
	const capable = (value) => {
		try {
			return value && typeof value === "object" && [
				"getHostId",
				"getConversation",
				"startTurn",
				"interruptConversation"
			].every((key) => typeof value[key] === "function") && value.getHostId() === "local";
		} catch {
			return false;
		}
	};
	let manager = host.__lazymindCodexManager;
	if (!capable(manager)) {
		const roots = [];
		if (host.__codexRoot?._internalRoot?.current) roots.push(host.__codexRoot._internalRoot.current);
		for (const element of Array.from(document.querySelectorAll("#root, #app, body > div"))) for (const key of Object.keys(element)) if (key.startsWith("__reactContainer$") || key.startsWith("__reactFiber$")) roots.push(element[key]);
		const fibers = /* @__PURE__ */ new Set(), values = /* @__PURE__ */ new Set();
		const find = (value, depth) => {
			try {
				if (!value || typeof value !== "object" || values.has(value) || values.size > 2e4) return null;
				values.add(value);
				if (capable(value)) return value;
				const bindings = value.node?.familyBindings;
				if (bindings instanceof Map) {
					for (const family of bindings.values()) {
						if (!(family instanceof Map)) continue;
						for (const binding of family.values()) {
							const cell = binding?.value;
							if (cell?.atom && cell.store && typeof cell.get === "function") try {
								const current = cell.get();
								if (capable(current)) return current;
							} catch {}
						}
					}
					return null;
				}
				if (!depth || value instanceof Node || value.$$typeof || "tag" in value && "memoizedState" in value) return null;
				const entries = value instanceof Map ? Array.from(value.values()) : Object.values(Object.getOwnPropertyDescriptors(value)).flatMap((d) => "value" in d ? [d.value] : []);
				for (const entry of entries) {
					const found = find(entry, depth - 1);
					if (found) return found;
				}
			} catch {}
			return null;
		};
		while (roots.length && fibers.size < 3e4 && !manager) {
			const fiber = roots.pop();
			if (!fiber || fibers.has(fiber)) continue;
			fibers.add(fiber);
			if (fiber.sibling) roots.push(fiber.sibling);
			if (fiber.child) roots.push(fiber.child);
			let hook = fiber.memoizedState;
			for (let i = 0; hook && i < 160 && !manager; i++, hook = hook.next) manager = find(hook.memoizedState, 2);
			if (!manager) manager = find(fiber.memoizedProps?.value, 2);
			let dependency = fiber.dependencies?.firstContext;
			for (let i = 0; dependency && i < 30 && !manager; i++, dependency = dependency.next) manager = find(dependency.memoizedValue, 2);
		}
		if (!capable(manager)) throw new Error("Codex native conversation interface is unavailable");
		host.__lazymindCodexManager = manager;
	}
	if (input.kind === "probe") return { ready: true };
	const raw = manager.getConversation(input.threadId);
	if (!raw || raw.id !== input.threadId) return { missing: true };
	const snapshot = () => {
		const state = manager.getConversation(input.threadId);
		const history = state?.turnHistory;
		const turns = (history?.kind === "canonical" ? history.history.islands.flatMap((island) => island.entries.map((entry) => history.history.entitiesByKey[entry.value])).filter(Boolean) : state?.turns ?? []).map((turn) => ({
			id: turn.turnId ?? turn.id,
			status: turn.status,
			startedAt: turn.turnStartedAtMs ?? turn.startedAtMs,
			items: turn.items ?? []
		}));
		const role = manager.getStreamRole?.(input.threadId)?.role;
		const ready = state.resumeState === "resumed" && ["owner", "follower"].includes(role) && manager.isConversationStreaming?.(input.threadId) === true;
		return {
			id: state.id,
			turns,
			ready,
			owner: role === "owner",
			active: state.threadRuntimeStatus?.type === "active" || turns.some((turn) => turn.status === "inProgress")
		};
	};
	if (input.kind === "read") return snapshot();
	if (input.kind === "start") {
		if (!snapshot().ready) return {
			rejected: "Codex conversation is still restoring; no message was sent",
			retryable: true
		};
		if (snapshot().active) return { rejected: "Codex chat is busy; no message was sent" };
		const result = await manager.startTurn(input.threadId, {
			request: {
				threadId: input.threadId,
				clientUserMessageId: input.clientId,
				input: [{
					type: "text",
					text: input.message,
					text_elements: []
				}]
			},
			context: {
				inheritThreadSettings: true,
				useUserMessageAdmission: false,
				attachments: [],
				commentAttachments: []
			}
		});
		const turn = result?.turn ?? result?.result?.turn ?? result?.result?.result?.turn;
		if (turn?.id || result?.turnId) return {
			accepted: true,
			turnId: turn?.id ?? result.turnId
		};
		const matches = snapshot().turns.filter((turn$1) => turn$1.items.some((item) => item.type === "userMessage" && item.clientId === input.clientId));
		if (matches.length !== 1) throw new Error("Native turn receipt is uncertain");
		return {
			accepted: true,
			turnId: matches[0].id
		};
	}
	if (input.kind === "interrupt") {
		const target = snapshot().turns.find((turn) => turn.id === input.turnId);
		if (!target || target.status !== "inProgress") return { interruptedTurnId: null };
		if (manager.getStreamRole?.(input.threadId)?.role !== "owner" || JSON.stringify(readEvidence(target)) !== input.expectedOwnership) return { rejected: "Codex execution ownership changed; no interruption was sent" };
		return { interruptedTurnId: await manager.interruptConversation(input.threadId, "user-stop", input.turnId) ?? null };
	}
	throw new Error("Unknown Codex operation");
}
function nativeExpression(input) {
	return `(input => (${codexOperation.toString()})(input, ${workflowTurnEvidence.toString()}))(${JSON.stringify(input)})`;
}
var CodexNative = class {
	constructor(port) {
		this.port = port;
	}
	evaluate(id, input, signal) {
		return windowCdp(this.port, id).evaluate(nativeExpression(input), signal, input.kind === "start" ? 3e4 : 8e3);
	}
	async probe(signal) {
		const windows = await codexTargets(this.port, signal);
		return {
			ready: (await Promise.allSettled(windows.map((window$1) => this.evaluate(window$1.id, { kind: "probe" }, signal)))).some((result) => result.status === "fulfilled" && result.value?.ready === true),
			window_count: windows.length
		};
	}
	async locate(threadId, signal) {
		const windows = await codexTargets(this.port, signal);
		const matches = (await Promise.allSettled(windows.map(async (window$1) => ({
			id: window$1.id,
			state: await this.evaluate(window$1.id, {
				kind: "read",
				threadId
			}, signal)
		})))).flatMap((result) => result.status === "fulfilled" && !result.value.state?.missing && result.value.state?.id === threadId ? [result.value] : []);
		const owners = matches.filter((match) => match.state.owner);
		if (owners.length > 1) throw Error("Codex reports conflicting owners for this chat");
		const selected = owners[0] ?? matches.sort((a, b) => a.id.localeCompare(b.id))[0];
		if (!selected) throw Error("Open the original Codex chat before continuing");
		return selected;
	}
	async read(threadId, signal) {
		return (await this.locate(threadId, signal)).state;
	}
	async visible(threadId, signal) {
		const windows = await codexTargets(this.port, signal);
		return (await Promise.allSettled(windows.map((window$1) => windowCdp(this.port, window$1.id).evaluate(sessionExpression(), signal)))).some((result) => result.status === "fulfilled" && result.value?.session_id === threadId);
	}
	async presented(threadId, signal) {
		const windows = await codexTargets(this.port, signal);
		const expression = `(async()=>{const panel=window.__lazymindCodexWorkflowTail;if(panel?.session!==${JSON.stringify(threadId)})return false;const state=await panel.probeStatus?.();return window.__lazymindCodexWorkflowTail===panel&&panel.session===${JSON.stringify(threadId)}&&state?.session_id===${JSON.stringify(threadId)}&&state?.status==='mounted'&&state?.page_status==='ready'})()`;
		return (await Promise.allSettled(windows.map((window$1) => windowCdp(this.port, window$1.id).evaluate(expression, signal)))).some((result) => result.status === "fulfilled" && result.value === true);
	}
	async mutate(input, signal) {
		const target = await this.locate(input.threadId, signal);
		return this.evaluate(target.id, input, signal);
	}
	start(threadId, clientId, message, signal) {
		return this.mutate({
			kind: "start",
			threadId,
			clientId,
			message
		}, signal);
	}
	interrupt(threadId, turnId, expectedOwnership, signal) {
		return this.mutate({
			kind: "interrupt",
			threadId,
			turnId,
			expectedOwnership
		}, signal);
	}
};

//#endregion
//#region src/receipts.ts
const threadPattern = /^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$/i;
/** Receipts contain identifiers only. A pre-write prevents replay after process death. */
var NativeReceipts = class {
	constructor(directory) {
		this.directory = directory;
	}
	path(id) {
		return join(this.directory, createHash("sha256").update(id).digest("hex") + ".json");
	}
	async read(id) {
		try {
			const value = JSON.parse(await readFile(this.path(id), "utf8"));
			if (value.actionId !== id || !value.runId || !threadPattern.test(value.threadId)) throw new Error("Invalid native receipt");
			return value;
		} catch (error) {
			if (error.code === "ENOENT") return void 0;
			throw error;
		}
	}
	async write(receipt) {
		await mkdir(this.directory, {
			recursive: true,
			mode: 448
		});
		const path = this.path(receipt.actionId), temp = `${path}.${randomUUID()}.tmp`;
		await writeFile(temp, JSON.stringify(receipt), {
			mode: 384,
			flag: "wx"
		});
		await rename(temp, path);
	}
};

//#endregion
//#region src/connection-recovery.ts
function connectionRecovery(base, pairing, signal) {
	let reported = false;
	return {
		available() {
			reported = false;
		},
		async unavailable(thread) {
			if (reported || signal.aborted) return;
			reported = true;
			try {
				await setTimeout$1(350, void 0, { signal });
				if (!(await fetch(new URL("/v1/codex/recovery", base), {
					method: "POST",
					redirect: "error",
					signal: AbortSignal.any([signal, AbortSignal.timeout(12e3)]),
					headers: {
						Authorization: `Bearer ${pairing.token}`,
						"X-LazyMind-Connector-Id": pairing.connector_id,
						"Content-Type": "application/json"
					},
					body: JSON.stringify({
						code: "CODEX_CONNECTION_CHECK",
						thread_id: thread
					})
				})).ok) reported = false;
			} catch {
				reported = false;
			}
		}
	};
}

//#endregion
//#region src/recovery-delivery.ts
const threadID = /^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$/i;
const identifier = /^[a-f0-9]{32}$/;
const tools = new Set([
	"workflow.start",
	"workflow.step.begin",
	"workflow.step.claim",
	"workflow.step.resume"
]);
function recoveryMessage(request, intent) {
	if (!identifier.test(request) || !identifier.test(intent.id ?? "") || !threadID.test(intent.thread_id) || !tools.has(intent.tool ?? "") || !intent.arguments || typeof intent.arguments !== "object" || Array.isArray(intent.arguments)) throw Error("Invalid saved Codex recovery operation");
	const args = intent.arguments;
	if (intent.tool === "workflow.start" && (typeof args.idempotency_key !== "string" || args.idempotency_key.length < 16 || args.driver_session_id !== intent.thread_id || typeof args.workflow_id !== "string")) throw Error("Missing stable Workflow start identity");
	if (intent.tool === "workflow.step.begin" && (typeof args.command_id !== "string" || !args.command_id)) throw Error("Missing stable Workflow command identity");
	return `[LazyMind recovery_id=${request} operation_id=${intent.id}]\nThe user requested the operation below before Codex session control was unavailable, then chose Restart and connect in LazyMind. LazyMind saved this operation BEFORE any Workflow execution mutation. The restart ended the old MCP call; this is its recovery handoff, not a new user task. Continue in this original conversation. Do not ask the user to type Continue again. Preserve every supplied identifier and argument.
Saved operation (JSON data): ${JSON.stringify({
		tool: intent.tool,
		arguments: args
	})}\n` + (intent.tool === "workflow.start" ? "Retry workflow.start with these exact arguments and the SAME idempotency_key. Use the returned existing/new session. Do not create a second run or invent another retry key. " : "Read workflow.state for the saved session_id first. Reconcile the saved execution/command with current state; use an existing successful result instead of executing it again. For workflow.step.begin, proceed only when current control admission permits it and the saved step is ready or retryable; keep the SAME command_id. For workflow.step.claim or workflow.step.resume, use the SAME execution_id, never begin a replacement execution. ") + "Continue using the existing Workflow skill and returned execution contract. The bound panel appears automatically. Keep normal review rules: yield when awaiting_user, stopped, completed, failed or otherwise not admitted; restarting is not approval of a Workflow review.";
}
/** One-shot, invoked only by the LazyMind recovery child after the user's click.
* Write before native admission; an uncertain send is reconciled, never replayed.
*/
async function deliverRecovery(request, intent, native, receipts, signal, pause = (signal$1) => setTimeout$1(250, void 0, { signal: signal$1 }), authorize = async () => {}, stage = (_stage) => {}) {
	if (!identifier.test(request) || !threadID.test(intent.thread_id)) throw Error("Invalid Codex recovery target");
	const message = intent.tool ? recoveryMessage(request, intent) : void 0;
	const actionId = `recovery:${request}:${intent.id}`;
	const saved = message ? await receipts.read(actionId) : void 0;
	const previous = saved?.status === "rejected" ? void 0 : saved;
	if (previous && (previous.threadId !== intent.thread_id || previous.runId !== request)) throw Error("Recovery receipt does not match this conversation");
	let state;
	stage("waiting-chat");
	for (;;) {
		signal.throwIfAborted();
		try {
			if (!message) {
				stage("showing-panel");
				if (await native.presented(intent.thread_id, signal)) return { accepted: true };
				await pause(signal);
				continue;
			}
			if (!await native.visible(intent.thread_id, signal)) {
				await pause(signal);
				continue;
			}
			state = await native.read(intent.thread_id, signal);
		} catch (error) {
			if (signal.aborted) throw error;
			await pause(signal);
			continue;
		}
		if (previous?.status === "accepted") return { accepted: true };
		if (state.ready === false) {
			await pause(signal);
			continue;
		}
		if (previous) {
			const matches = state.turns.filter((turn) => turn.items.some((item) => item.type === "userMessage" && item.clientId === previous.clientId));
			if (matches.length === 1) {
				await receipts.write({
					...previous,
					turnId: matches[0].id,
					status: "accepted"
				});
				return { accepted: true };
			}
			throw Error("Recovery delivery is uncertain; no duplicate request was sent");
		}
		if (!state.active) break;
		stage("waiting-idle");
		await pause(signal);
	}
	const receipt = {
		actionId,
		runId: request,
		threadId: intent.thread_id,
		kind: "continue",
		clientId: randomUUID(),
		status: "sending",
		turnId: void 0
	};
	signal.throwIfAborted();
	await authorize();
	signal.throwIfAborted();
	await receipts.write(receipt);
	let retrying = false;
	try {
		stage("resuming");
		const result = await native.start(intent.thread_id, receipt.clientId, message, signal);
		if (result?.rejected) {
			receipt.status = "rejected";
			if (result.retryable) {
				await receipts.write(receipt);
				stage("waiting-chat");
				await pause(signal);
				retrying = true;
				return await deliverRecovery(request, intent, native, receipts, signal, pause, authorize, stage);
			}
			throw Error(result.rejected);
		}
		if (!result?.accepted || !result.turnId || state.turns.some((turn) => turn.id === result.turnId)) throw Error("Recovery delivery receipt is uncertain");
		receipt.turnId = result.turnId;
		receipt.status = "accepted";
		await receipts.write(receipt);
		return { accepted: true };
	} catch (error) {
		if (retrying) throw error;
		if (receipt.status !== "rejected") receipt.status = "unknown";
		await receipts.write(receipt);
		throw error;
	}
}

//#endregion
//#region src/recovery-result.ts
async function finishRecovery(result) {
	await new Promise((resolve, reject) => {
		process.stdout.write(JSON.stringify(result) + "\n", (error) => error ? reject(error) : resolve());
	});
	process.exit(0);
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
		"cdp-port": { type: "string" },
		"parent-pid": { type: "string" },
		"cleanup-only": {
			type: "boolean",
			default: false
		},
		"probe-only": {
			type: "boolean",
			default: false
		},
		"presentation-probe-only": {
			type: "boolean",
			default: false
		},
		"thread-id": { type: "string" },
		"recover-intent": {
			type: "boolean",
			default: false
		}
	} });
	if (values["presentation-probe-only"]) {
		const thread = values["thread-id"] ?? "";
		if (!/^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$/i.test(thread)) throw Error("Invalid presentation target");
		return finishRecovery({ ready: await new CodexNative(Number(values["cdp-port"])).presented(thread, AbortSignal.timeout(5e3)) });
	}
	if (values["probe-only"]) {
		console.log(JSON.stringify(await new CodexNative(Number(values["cdp-port"])).probe(AbortSignal.timeout(4e3))));
		return;
	}
	const path = values["pairing-file"];
	if (!path) throw new Error("pairing-file is required");
	const pairing = JSON.parse(await readFile(path, "utf8"));
	if (pairing.provider !== "codex" || !pairing.enabled && !values["cleanup-only"]) throw new Error("Codex pairing is disabled");
	if (values["recover-intent"]) {
		const chunks = [];
		let size = 0;
		for await (const chunk of process.stdin) {
			size += chunk.length;
			if (size > 256 * 1024) throw Error("Recovery operation is too large");
			chunks.push(Buffer.from(chunk));
		}
		const input = JSON.parse(Buffer.concat(chunks).toString("utf8"));
		const signal$1 = AbortSignal.timeout(6e4);
		const authorize = async () => {
			const response = await fetch(new URL("/v1/codex/recovery?wait=0", values["bridge-url"]), {
				signal: AbortSignal.any([signal$1, AbortSignal.timeout(5e3)]),
				redirect: "error",
				headers: {
					Authorization: `Bearer ${pairing.token}`,
					"X-LazyMind-Connector-Id": pairing.connector_id
				}
			});
			if (!response.ok) throw Error("Codex recovery authorization is no longer valid");
			const latest = await response.json();
			if (latest.request?.id !== input.request_id || !latest.request.intents?.some((intent) => intent.id === input.intent.id && intent.thread_id === input.intent.thread_id && intent.tool === input.intent.tool && JSON.stringify(intent.arguments) === JSON.stringify(input.intent.arguments))) throw Error("Codex recovery request expired");
		};
		return finishRecovery(await deliverRecovery(input.request_id, input.intent, new CodexNative(Number(values["cdp-port"])), new NativeReceipts(join(dirname(path), pairing.connector_id + ".recovery-receipts")), signal$1, void 0, authorize, (stage) => console.log(JSON.stringify({ stage }))));
	}
	if (values["cleanup-only"]) {
		const deadline = Date.now() + 3e3;
		let release;
		while (!release) try {
			release = await dispatcherLock(path, randomUUID());
		} catch (error) {
			if (!(error instanceof DispatcherBusy) || Date.now() >= deadline) throw error;
			await setTimeout$1(100);
		}
		try {
			await cleanupCodexPresentations(Number(values["cdp-port"]), AbortSignal.timeout(2e3));
		} finally {
			await release();
		}
		return;
	}
	const native = new CodexNative(Number(values["cdp-port"]));
	const lifetime = new AbortController(), signal = lifetime.signal;
	const stop = () => lifetime.abort();
	process.once("SIGINT", stop);
	process.once("SIGTERM", stop);
	const instance = randomUUID(), unlock = await dispatcherLock(path, instance);
	const bridge = new HostBridge(values["bridge-url"], pairing);
	const adapter = new CodexAdapter(native, new NativeReceipts(join(dirname(path), pairing.connector_id + ".receipts")), bridge, signal, connectionRecovery(values["bridge-url"], pairing, signal));
	let presentation = {
		status: "starting",
		checked_at: Date.now()
	};
	const lookup = async (session) => {
		const response = await fetch(new URL("/v1/codex/presentation?session_id=" + encodeURIComponent(session), values["bridge-url"]), {
			signal: AbortSignal.any([signal, AbortSignal.timeout(15e3)]),
			redirect: "error",
			headers: {
				Authorization: `Bearer ${pairing.token}`,
				"X-LazyMind-Connector-Id": pairing.connector_id
			}
		});
		if (!response.ok) throw Object.assign(/* @__PURE__ */ new Error("Codex presentation lookup unavailable"), { status: response.status });
		return response.json();
	};
	const parent = Number(values["parent-pid"]);
	if (!Number.isSafeInteger(parent) || parent < 1) throw new Error("parent-pid is required");
	const timer = setInterval(async () => {
		try {
			process.kill(parent, 0);
			const latest = JSON.parse(await readFile(path, "utf8"));
			if (!latest.enabled || latest.token !== pairing.token) stop();
		} catch {
			stop();
		}
	}, 1e3);
	async function heartbeat() {
		const file = path + ".codex-status.json", temp = file + "." + instance;
		while (!signal.aborted) {
			let ready = false, issue = "host_unavailable";
			try {
				ready = (await native.probe(signal)).ready === true;
				issue = ready ? "" : "capability_unavailable";
			} catch {
				issue = "cdp_unavailable";
			}
			try {
				await writeFile(temp, JSON.stringify({
					connector_id: pairing.connector_id,
					checked_at: Date.now(),
					ready,
					issue,
					presentation
				}), { mode: 384 });
				await rename(temp, file);
			} catch {}
			try {
				await setTimeout$1(2e3, void 0, { signal });
			} catch {
				break;
			}
		}
	}
	try {
		await Promise.all([
			heartbeat(),
			createDispatcher(adapter, adapter.host, bridge, instance, signal).poll(),
			presentationLoop(Number(values["cdp-port"]), lookup, values["bridge-url"], signal, (value) => {
				presentation = value;
			})
		]);
	} finally {
		stop();
		clearInterval(timer);
		await unlock();
		process.off("SIGINT", stop);
		process.off("SIGTERM", stop);
	}
}
main().catch((error) => {
	console.error(String(error));
	process.exitCode = 1;
});

//#endregion
export {  };