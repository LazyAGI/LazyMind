import { open, readFile, stat, unlink } from "node:fs/promises";
import { homedir } from "node:os";
import { isAbsolute, join, resolve } from "node:path";
import { parseArgs, promisify } from "node:util";
import { randomUUID } from "node:crypto";
import { execFile } from "node:child_process";
import { setTimeout as setTimeout$1 } from "node:timers/promises";

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
//#region ../workflow-agent-core/src/protocol.ts
function object(value) {
	return typeof value === "object" && value !== null && !Array.isArray(value) ? value : null;
}
function interaction(value, trustedOrigin) {
	const result = object(object(value)?.structuredContent);
	const fields = typeof result?.session_id === "string" ? result : object(result?.state);
	if (!fields || typeof fields.session_id !== "string" || !fields.session_id || typeof fields.interaction_url !== "string") return null;
	try {
		const url = new URL(fields.interaction_url);
		if (!["http:", "https:"].includes(url.protocol) || url.username || url.password || url.hash || url.search) return null;
		if (trustedOrigin && url.origin !== new URL(trustedOrigin).origin) return null;
		if (url.pathname !== `/workflow-runs/${encodeURIComponent(fields.session_id)}`) return null;
		return {
			runId: fields.session_id,
			url: url.href
		};
	} catch {
		return null;
	}
}
function readControl(value) {
	const fields = object(object(value)?.structuredContent);
	const control = object(fields?.control) ?? object(object(fields?.state)?.control);
	const admission = object(control?.admission);
	if (control?.protocol !== "workflow.control.v1" || typeof control.session_id !== "string" || !Number.isSafeInteger(control.state_version) || typeof control.continuation !== "string" || typeof admission?.can_begin !== "boolean") return null;
	return control;
}

//#endregion
//#region ../workflow-agent-core/src/coordinator.ts
const READS = new Set([
	"list",
	"get",
	"input_get",
	"state",
	"session_list",
	"artifact_list",
	"artifact_get"
]);
const ACQUIRE = new Set([
	"step_begin",
	"step_claim",
	"step_resume"
]);
const PAUSED = new Set([
	"awaiting_user",
	"awaiting_executor",
	"draining",
	"stopped",
	"binding_required"
]);
/** Coordinates host execution using Core decisions; does not proxy MCP calls. */
function createCoordinator(runtime, bridge, webUrl, lifetime) {
	const scopes = /* @__PURE__ */ new Map();
	const ptcLinks = /* @__PURE__ */ new Map();
	const actionCache = /* @__PURE__ */ new Map();
	const signal = (caller) => caller ? AbortSignal.any([caller, lifetime]) : lifetime;
	const driver = (agent) => {
		const visited = /* @__PURE__ */ new Set();
		while (!visited.has(agent)) {
			visited.add(agent);
			const parent = runtime.parent?.(agent);
			if (!parent) return agent;
			agent = parent;
		}
		throw new Error("Workflow driver ownership contains a cycle");
	};
	const paused = (scope) => scope.unknown || !!scope.control && PAUSED.has(scope.control.continuation);
	const completion = (scope) => driver(scope.agent) !== scope.agent && scope.returnPending && !!runtime.canReturnResult?.(scope.agent);
	function publish(runId, control) {
		if (control.protocol !== "workflow.control.v1" || control.session_id !== runId) throw new Error("Invalid workflow control response");
		for (const scope of scopes.values()) {
			if (scope.runId !== runId) continue;
			scope.unknown = false;
			if (scope.control && scope.control.state_version > control.state_version) continue;
			scope.control = control;
			if (control.active_execution_ids) {
				for (const id of scope.grants) if (!control.active_execution_ids.includes(id) || control.native_execution_ids?.includes(id)) scope.grants.delete(id);
			}
		}
	}
	function suspendGoal(scope) {
		if (!scope.runId || !scope.automatic || driver(scope.agent) !== scope.agent || !scope.control || !PAUSED.has(scope.control.continuation)) return;
		runtime.suspendGoal?.(scope.agent, {
			runId: scope.runId,
			goalId: scope.goalId,
			continuation: scope.control.continuation
		});
	}
	function resumeGoal(scope) {
		if (scope.runId) runtime.resumeGoal?.(scope.agent, scope.runId);
	}
	function remember(scope) {
		const root = driver(scope.agent);
		const finished = /* @__PURE__ */ new Set();
		let ownedAt = 0;
		let ownedSeq = -1;
		let latestInputSeq = -1;
		for (const event of [...runtime.history?.(scope.agent) ?? []].reverse()) {
			if (event.user && latestInputSeq < 0) latestInputSeq = event.seq;
			const run = event.run;
			if (!run || run.hostSessionId !== runtime.id(root) || !run.operation || ![
				"start",
				"step_begin",
				"step_claim",
				"step_resume",
				"step_complete"
			].includes(run.operation)) continue;
			if (!scope.runId) {
				scope.runId = run.runId;
				scope.automatic = true;
				ownedAt = event.time;
				ownedSeq = event.seq;
			}
			if (run.runId !== scope.runId || !run.executionId) continue;
			if (run.operation === "step_complete") finished.add(run.executionId);
			else if (ACQUIRE.has(run.operation) && !finished.has(run.executionId)) scope.grants.add(run.executionId);
		}
		if (latestInputSeq > ownedSeq) scope.automatic = false;
		scope.activeOwned = !!runtime.isRunning?.(scope.agent) && scope.automatic;
		if (!scope.runId && root !== scope.agent) scope.runId = ensure(root).runId;
		const goal = runtime.goal?.(root);
		if (goal && ownedAt && goal.createdAt <= ownedAt) scope.goalId = goal.id;
		else if (goal && ownedAt && goal.createdAt > ownedAt) scope.automatic = false;
	}
	function denial(scope, exec) {
		const operation = exec.operation;
		if (operation && (READS.has(operation) || operation === "session_stop")) return void 0;
		if (!scope.runId) return void 0;
		if (exec.returnsResult && completion(scope)) return void 0;
		if (scope.unknown && (scope.activeOwned || operation)) return "Workflow state is unavailable; retry after reconnecting LazyMind.";
		if (!paused(scope)) return void 0;
		if (scope.control?.continuation === "stopped") return operation || scope.activeOwned ? "This Workflow has been stopped." : void 0;
		if (operation === "artifact_publish" || operation === "step_complete" || operation === "step_resume" || operation === "step_claim") {
			const id = object(exec.arguments)?.execution_id;
			return typeof id === "string" && scope.control?.active_execution_ids?.includes(id) ? void 0 : "Only an already granted execution may finish while review is pending.";
		}
		if (operation) return "Review the submitted artifacts in the LazyMind panel before starting new Workflow work.";
		if (scope.grants.size > 0 || scope.manual) return void 0;
		if (scope.activeOwned) return "This Workflow is waiting for user review.";
	}
	function ensure(agent) {
		const existing = scopes.get(agent);
		if (existing) return existing;
		const scope = {
			agent,
			unknown: false,
			grants: /* @__PURE__ */ new Set(),
			manual: false,
			activeOwned: false,
			automatic: false,
			returnPending: false
		};
		scopes.set(agent, scope);
		remember(scope);
		return scope;
	}
	async function refresh(scope, caller) {
		if (!scope.runId) return void 0;
		try {
			let state = await bridge.state(scope.runId, signal(caller));
			const root = driver(scope.agent);
			if (state.continuation === "binding_required" && !state.binding?.bound && !state.binding?.driver_session_id && (runtime.history?.(root) ?? []).some((event) => {
				const run = event.run;
				return !!run && run.runId === scope.runId && run.operation === "start" && run.hostSessionId === runtime.id(root);
			})) state = await bridge.bind(scope.runId, runtime.id(root), signal(caller));
			if (state.binding?.driver_session_id && state.binding.driver_session_id !== runtime.id(driver(scope.agent))) {
				scope.control = void 0;
				scope.runId = void 0;
				scope.grants.clear();
				return;
			}
			publish(scope.runId, state);
			return scope.control;
		} catch (error) {
			scope.unknown = true;
			throw error;
		}
	}
	async function afterResult(value, exec) {
		if (!exec.agent || runtime.isLive?.(exec.agent) === false || lifetime.aborted) return null;
		const scope = ensure(exec.agent);
		const root = driver(exec.agent);
		const rootScope = ensure(root);
		const operation = exec.operation;
		const returned = readControl(value);
		const fields = object(object(value)?.structuredContent);
		const run = interaction(value, webUrl) ?? (returned ? {
			runId: returned.session_id,
			url: new URL(`/workflow-runs/${encodeURIComponent(returned.session_id)}`, webUrl).href
		} : null);
		const runId = returned?.session_id ?? run?.runId;
		if (!runId) return null;
		if (operation && READS.has(operation) && runId !== scope.runId && runId !== rootScope.runId) return returned;
		const execution = object(fields?.execution);
		if (operation && ACQUIRE.has(operation) && typeof execution?.execution_id === "string") {
			if (execution.executor_host !== "lazymind") scope.grants.add(execution.execution_id);
			scope.returnPending = execution.executor_host === "lazymind";
			scope.activeOwned = scope.automatic = true;
			scope.manual = false;
		}
		if (operation === "step_complete" && typeof fields?.execution_id === "string") {
			scope.grants.delete(fields.execution_id);
			scope.returnPending = true;
			scope.activeOwned = scope.automatic = true;
			scope.manual = false;
		}
		if (operation === "start") {
			scope.activeOwned = scope.automatic = rootScope.activeOwned = rootScope.automatic = true;
			scope.manual = rootScope.manual = false;
		}
		try {
			const fresh = operation === "start" ? await bridge.bind(runId, runtime.id(root), signal(exec.signal)) : returned ?? await bridge.state(runId, signal(exec.signal));
			if (fresh.binding?.driver_session_id !== runtime.id(root)) return null;
			if (rootScope.runId && rootScope.runId !== runId && operation !== "start") {
				if (scope !== rootScope) {
					scope.runId = runId;
					publish(runId, fresh);
					return scope.control ?? fresh;
				}
				return null;
			}
			scope.runId = rootScope.runId = runId;
			if (operation === "start") {
				rootScope.activeOwned = rootScope.automatic = true;
				rootScope.manual = false;
				rootScope.goalId = runtime.goal?.(root)?.id;
			}
			publish(runId, fresh);
			suspendGoal(rootScope);
			if (exec.nested && run) ptcLinks.set(exec.callId, {
				...run,
				hostSessionId: runtime.id(root),
				operation: operation ?? void 0,
				...typeof fields?.execution_id === "string" ? { executionId: fields.execution_id } : typeof execution?.execution_id === "string" ? { executionId: execution.execution_id } : {}
			});
			return scope.control ?? fresh;
		} catch (error) {
			scope.runId = runId;
			scope.unknown = true;
			if (!rootScope.runId || rootScope.runId === runId || operation === "start") {
				rootScope.runId = runId;
				rootScope.unknown = true;
			}
			runtime.warn(`lazymind-workflow: result committed, control synchronization failed: ${String(error)}`);
			return null;
		}
	}
	async function lookupInput(agent, messages, caller) {
		for (const message of messages) {
			const source = message;
			if (!source.user || typeof source.requestId !== "string") continue;
			let claim = actionCache.get(source.requestId);
			try {
				claim = await bridge.action(source.requestId, signal(caller));
			} catch (error) {
				if (error instanceof BridgeError && (error.status === 404 || error.status === 403)) continue;
				if (error instanceof BridgeError && error.code === "BINDING_STALE") throw error;
				if (!claim) continue;
			}
			if (claim && claim.action.native_session_id === runtime.id(agent)) {
				actionCache.set(source.requestId, claim);
				return claim;
			}
		}
	}
	async function beforeTurn(payload) {
		const scope = ensure(payload.agent);
		if (scope.turn !== payload.turn) {
			scope.turn = payload.turn;
			scope.manual = false;
			scope.activeOwned = scope.automatic;
			scope.actionId = void 0;
		}
		try {
			const action = await lookupInput(payload.agent, payload.messages, payload.signal);
			if (action) {
				if (action.action.status === "superseded" || action.action.binding_generation !== action.control.binding?.generation || action.action.consumed_at && scope.actionId !== action.action.id) return false;
				scope.runId = action.action.session_id;
				scope.actionId = action.action.id;
				scope.activeOwned = scope.automatic = true;
				scope.manual = false;
				publish(scope.runId, action.control);
			} else if (payload.messages.some((message) => message.user)) {
				scope.manual = true;
				scope.activeOwned = scope.automatic = false;
			}
			if ((!scope.manual || scope.activeOwned) && !completion(scope)) await refresh(scope, payload.signal);
			suspendGoal(ensure(driver(scope.agent)));
			if (scope.runId && scope.activeOwned && paused(scope) && !scope.manual && scope.grants.size === 0 && !completion(scope)) return false;
			return true;
		} catch (error) {
			runtime.warn(`lazymind-workflow: control gate deferred a step: ${String(error)}`);
			return false;
		}
	}
	async function beforeTool(exec) {
		if (!exec.agent) return void 0;
		const scope = ensure(exec.agent);
		if (exec.returnsResult && completion(scope)) return void 0;
		const operation = exec.operation;
		const runId = object(exec.arguments)?.session_id;
		if (operation && !READS.has(operation) && typeof runId === "string" && runId !== scope.runId) try {
			const fresh = await bridge.state(runId, signal(exec.signal));
			if (fresh.binding?.driver_session_id !== runtime.id(driver(exec.agent))) return "This Workflow belongs to another driver session.";
			scope.runId = runId;
			publish(runId, fresh);
		} catch (error) {
			return `Workflow state unavailable: ${String(error)}`;
		}
		if (scope.runId && (scope.activeOwned || operation && !READS.has(operation))) try {
			await refresh(scope, exec.signal);
		} catch {
			return "Workflow state is unavailable; reconnect LazyMind.";
		}
		return denial(scope, exec);
	}
	return {
		ensure,
		driver,
		publish,
		suspendGoal,
		resumeGoal,
		afterResult,
		beforeTurn,
		beforeTool,
		cacheClaim(claim) {
			actionCache.set(claim.action.id, claim);
		},
		denial(agent, exec) {
			return denial(ensure(agent), exec);
		},
		shouldConclude(agent, control) {
			const scope = ensure(agent);
			return scope.activeOwned && !completion(scope) && (scope.unknown || !!scope.runId && ["awaiting_user", "awaiting_executor"].includes(control?.continuation ?? ""));
		},
		takeNestedLink(callId) {
			const link = ptcLinks.get(callId);
			ptcLinks.delete(callId);
			return link;
		},
		idle(agent) {
			const scope = scopes.get(agent);
			if (scope) {
				scope.activeOwned = false;
				scope.manual = false;
			}
		},
		forget(agent) {
			scopes.delete(agent);
		},
		dispose() {
			scopes.clear();
			ptcLinks.clear();
			actionCache.clear();
		}
	};
}

//#endregion
//#region ../workflow-agent-core/src/adapter.ts
/** Use only when the host definitely did not receive the input. */
var AdmissionRejected = class extends Error {};

//#endregion
//#region ../workflow-agent-core/src/dispatcher.ts
/** Polling and delivery policy are shared; only host admission/history live in the adapter. */
function createDispatcher(runtime, coordinator, bridge, instanceId, signal) {
	const { ensure, publish, suspendGoal, resumeGoal } = coordinator;
	async function deliver(action) {
		if (action.kind === "cancel" && action.status !== "pending") {
			if (runtime.supportsCancel === false) return;
			const current$1 = await bridge.action(action.id, signal);
			if (current$1.control.binding?.generation !== action.binding_generation || current$1.control.continuation !== "stopped") return;
			const resolved$1 = await runtime.resolve(action.native_session_id);
			if ("error" in resolved$1) return;
			const scope$1 = ensure(resolved$1.agent);
			if (scope$1.runId === action.session_id && (scope$1.activeOwned || scope$1.grants.size > 0)) await runtime.cancel(resolved$1.agent);
			if (scope$1.runId === action.session_id) suspendGoal(scope$1);
			const seq$1 = runtime.eventSeq?.(resolved$1.agent) ?? 0;
			if (seq$1 > 0) await bridge.settle(action.id, instanceId, "", "accepted", seq$1, "", signal);
			return;
		}
		if (action.kind === "continue" && action.status !== "pending") {
			const current$1 = await bridge.action(action.id, signal);
			if (![
				"pending",
				"dispatching",
				"unknown"
			].includes(current$1.action.status)) return;
			if (current$1.action.status !== "pending") {
				const seq$1 = await runtime.reconcile?.(action.native_session_id, action.id, signal) ?? 0;
				if (seq$1 > 0) {
					await bridge.settle(action.id, instanceId, "", "accepted", seq$1, "", signal);
					return;
				}
			}
		}
		const claim = await bridge.claim(action.id, instanceId, signal);
		coordinator.cacheClaim(claim);
		if (!claim.dispatch_token || claim.action.status !== "dispatching") return;
		const resolved = await runtime.resolve(action.native_session_id);
		if ("error" in resolved) {
			await bridge.settle(action.id, instanceId, claim.dispatch_token, "failed", 0, resolved.error, signal);
			return;
		}
		const scope = ensure(resolved.agent);
		if (scope.runId === action.session_id) publish(action.session_id, claim.control);
		const current = await bridge.action(action.id, signal);
		if (current.action.consumed_at || current.action.status !== "dispatching" || current.control.binding?.generation !== action.binding_generation) return;
		if (!(action.kind === "cancel" ? current.control.continuation === "stopped" : action.execution_id ? !["stopped", "binding_required"].includes(current.control.continuation) : current.control.continuation === "continue" && current.control.admission.can_begin)) {
			await bridge.settle(action.id, instanceId, claim.dispatch_token, "failed", 0, "Workflow control changed before host admission", signal);
			return;
		}
		if (action.kind === "cancel" && runtime.supportsCancel === false) {
			await bridge.settle(action.id, instanceId, claim.dispatch_token, "failed", 0, "This host does not support interrupting the current turn; Workflow is stopped in Core.", signal);
			return;
		}
		let seq = 0;
		try {
			if (action.kind === "cancel") {
				if (scope.runId === action.session_id && (scope.activeOwned || scope.grants.size > 0)) await runtime.cancel(resolved.agent);
				if (scope.runId === action.session_id) suspendGoal(scope);
			} else {
				if (runtime.continuationMode !== "queue" && !action.execution_id && scope.runId === action.session_id && scope.activeOwned && scope.grants.size === 0) await runtime.cancel(resolved.agent);
				seq = await runtime.prompt(resolved.agent, {
					actionId: action.id,
					message: action.execution_id ? `LazyMind workflow ${action.session_id} has an execution update. Call workflow.state, then workflow.step.claim with execution_id=${action.execution_id}. If an execution_handle is returned, execute the granted contract and submit with that handle. If executor_host is lazymind, only observe. Do not create a new workflow.` : `The user clicked Continue in the LazyMind panel for workflow ${action.session_id} and has finished the current review. Call workflow.state, then workflow.step.begin for a ready step when control.continuation=continue and admission.can_begin=true. A human step requires review AFTER execution; its mode or requires_approval flag does not require another confirmation before begin. Continue until awaiting_user, awaiting_executor, stopped, or completed. Execute each granted step_contract and submit using execution_handle. Do not ask the user to confirm the review again or create a new workflow.`
				}, signal);
				if (scope.runId === action.session_id) resumeGoal(scope);
			}
			if (action.kind === "cancel") seq = runtime.eventSeq?.(resolved.agent) ?? 0;
			await bridge.settle(action.id, instanceId, claim.dispatch_token, "accepted", seq, "", signal);
		} catch (error) {
			await bridge.settle(action.id, instanceId, claim.dispatch_token, error instanceof AdmissionRejected ? "failed" : "unknown", 0, String(error), signal);
		}
	}
	async function poll() {
		while (!signal.aborted) {
			try {
				let after = "";
				do {
					const page = await bridge.actions(after, signal);
					for (const action of page.actions) try {
						await deliver(action);
					} catch (error) {
						if (!(error instanceof BridgeError && [
							"DELIVERY_PENDING",
							"ACTION_CONSUMED",
							"BINDING_STALE",
							"WORKFLOW_ADMISSION_DENIED"
						].includes(error.code)) && !signal.aborted) runtime.warn(`lazymind-workflow: delivery pending: ${String(error)}`);
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
	return new Promise((resolve$1, reject) => {
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
			resolve$1();
		}, ms);
		signal.addEventListener("abort", abort, { once: true });
	});
}

//#endregion
//#region src/runtime-lock.ts
var DispatcherBusy = class extends Error {};
/** One cooperating dispatcher per Codex profile pairing. */
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
		if (!Number.isSafeInteger(previous.pid) || previous.pid < 1) throw new Error("Invalid Codex dispatcher lock; repair this connection explicitly");
		try {
			process.kill(previous.pid, 0);
		} catch (error$1) {
			if (error$1.code === "ESRCH") {
				await unlink(path);
				continue;
			}
			throw error$1;
		}
		throw new DispatcherBusy("This Codex profile pairing already has a workflow dispatcher");
	}
	throw new Error("Could not acquire the Codex workflow dispatcher lock");
}

//#endregion
//#region src/adapter.ts
const runFile$1 = promisify(execFile);
const threadPattern = /^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$/i;
const queue = async (binary, args, signal, codexHome) => {
	await runFile$1(binary, args, {
		signal,
		timeout: 15e3,
		maxBuffer: 1024 * 1024,
		env: {
			...process.env,
			CODEX_HOME: codexHome
		}
	});
};
/** Admission only: no native turn inspection, cancellation, or uncertain retries. */
var CodexAdapter = class {
	continuationMode = "queue";
	supportsCancel = false;
	constructor(binary, codexHome, run = queue) {
		this.binary = binary;
		this.codexHome = codexHome;
		this.run = run;
	}
	id(threadId) {
		return threadId;
	}
	warn(message) {
		console.warn(message);
	}
	async resolve(threadId) {
		return threadPattern.test(threadId) ? { agent: threadId } : { error: "A Codex thread UUID is required" };
	}
	async prompt(threadId, input, signal) {
		if ("error" in await this.resolve(threadId)) throw new Error("A Codex thread UUID is required");
		if (signal.aborted) throw new AdmissionRejected("Queue delivery was aborted before launch");
		const message = `[LazyMind action_id=${input.actionId}]\n${input.message}\nThis queued notification may be delayed. Read workflow.state before taking any action; current Core state and authorization override this notification. When control.continuation is awaiting_user, report that review is needed and END this turn. Do not approve the review yourself, begin another step, or poll while waiting. Also end this turn for awaiting_executor, stopped, completed, or binding_required. Wait for a new notification or explicit user input.`;
		try {
			await this.run(this.binary, [
				"queue",
				"--thread",
				threadId,
				"--message",
				message
			], signal, this.codexHome);
		} catch (error) {
			if ([
				"ENOENT",
				"EACCES",
				"ENOEXEC"
			].includes(error.code ?? "")) throw new AdmissionRejected("Codex queue executable could not be launched");
			throw new Error("Codex queue receipt is uncertain; do not automatically resend");
		}
		return 0;
	}
	cancel() {
		throw new Error("Codex queue does not support turn interruption");
	}
};

//#endregion
//#region src/main.ts
const runFile = promisify(execFile);
async function main() {
	const { values } = parseArgs({ options: {
		"managed": {
			type: "boolean",
			default: false
		},
		"parent-pid": { type: "string" },
		"codex-bin": { type: "string" },
		"codex-home": { type: "string" },
		"pairing-file": { type: "string" },
		"lazymind-cli": {
			type: "string",
			default: "lazymind"
		},
		"bridge-url": {
			type: "string",
			default: "http://127.0.0.1:19091"
		}
	} });
	const binary = values["codex-bin"];
	if (!binary || !isAbsolute(binary)) throw new Error("--codex-bin must be the absolute path to the desktop bundled Codex executable");
	const profile = resolve(values["codex-home"] ?? process.env.CODEX_HOME ?? join(homedir(), ".codex"));
	let path = values["pairing-file"];
	if (!path) {
		const { stdout } = await runFile(values["lazymind-cli"], [
			"internal",
			"codex-workflow-pair",
			"--codex-home",
			profile
		]);
		path = JSON.parse(stdout).pairing_file;
	}
	if (!path) throw new Error("LazyMind did not return a pairing file");
	const info = await stat(path);
	if (!info.isFile() || process.platform !== "win32" && (info.mode & 63) !== 0) throw new Error("Pairing must be a private file (0600)");
	const pairing = JSON.parse(await readFile(path, "utf8"));
	if (pairing.provider !== "codex" || pairing.enabled !== true || pairing.profile !== profile || !/^host-[a-f0-9]{32}$/.test(pairing.connector_id) || !/^[a-f0-9]{64}$/.test(pairing.token)) throw new Error("Pairing does not match this Codex profile");
	const lifetime = new AbortController();
	const stop = () => lifetime.abort();
	process.once("SIGINT", stop);
	process.once("SIGTERM", stop);
	const parentPID = values["parent-pid"] ? Number(values["parent-pid"]) : void 0;
	if (parentPID !== void 0 && (!Number.isSafeInteger(parentPID) || parentPID < 1)) throw new Error("Invalid parent PID");
	const parentWatch = parentPID === void 0 ? void 0 : setInterval(() => {
		try {
			process.kill(parentPID, 0);
		} catch (error) {
			if (error.code === "ESRCH") lifetime.abort();
		}
	}, 1e3);
	parentWatch?.unref();
	const instanceId = randomUUID();
	let unlock;
	try {
		while (!lifetime.signal.aborted) try {
			unlock = await dispatcherLock(path, instanceId);
			break;
		} catch (error) {
			if (!values.managed || !(error instanceof DispatcherBusy)) throw error;
			await setTimeout$1(1e3, void 0, { signal: lifetime.signal });
		}
		lifetime.signal.throwIfAborted();
		const bridge = new HostBridge(values["bridge-url"], pairing);
		const adapter = new CodexAdapter(binary, profile);
		const coordinator = createCoordinator(adapter, bridge, "", lifetime.signal);
		console.log(`Codex Workflow queue dispatcher ready. MCP pairing file: ${path}. Native turn interruption is unavailable.`);
		await createDispatcher(adapter, coordinator, bridge, instanceId, lifetime.signal).poll();
	} finally {
		lifetime.abort();
		if (parentWatch) clearInterval(parentWatch);
		if (unlock) await unlock();
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