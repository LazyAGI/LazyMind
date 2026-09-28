import { createHash, randomUUID } from "node:crypto";
import { open, readFile, stat, unlink } from "node:fs/promises";

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
function presentationRun(meta) {
	const value = object(object(meta)?.lazymind_workflow);
	if (!value) return null;
	const run = interaction({ structuredContent: {
		session_id: value.runId,
		interaction_url: value.url
	} });
	return run ? {
		...run,
		...typeof value.hostSessionId === "string" ? { hostSessionId: value.hostSessionId } : {},
		...typeof value.operation === "string" ? { operation: value.operation } : {},
		...typeof value.executionId === "string" ? { executionId: value.executionId } : {}
	} : null;
}

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
//#region src/bridge.ts
/** The only credential loaded is LazyMind's scoped local pairing, never DSH's signing key. */
async function loadPairing(path) {
	if (!path) throw new Error("Reconnect DeepSeek Harness from LazyMind to configure the workflow bundle");
	const info = await stat(path);
	if (!info.isFile() || process.platform !== "win32" && (info.mode & 63) !== 0) throw new Error("Workflow pairing must be a private file");
	const value = object(JSON.parse(await readFile(path, "utf8")));
	if (!value || typeof value.connector_id !== "string" || typeof value.token !== "string" || value.token.length !== 64 || value.enabled !== true) throw new Error("Workflow pairing is unavailable; reconnect DeepSeek Harness from LazyMind");
	return {
		connector_id: value.connector_id,
		token: value.token
	};
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

//#endregion
//#region src/events.ts
const OPERATIONS = new Set([
	"list",
	"get",
	"input_import",
	"input_get",
	"start",
	"state",
	"session_list",
	"session_stop",
	"session_resume",
	"step_begin",
	"step_claim",
	"step_resume",
	"step_complete",
	"artifact_publish",
	"artifact_list",
	"artifact_get"
]);
function workflowOperation(name, serverName) {
	const prefix = `mcp__${serverName}__workflow_`;
	if (!name.startsWith(prefix)) return null;
	const operation = name.slice(prefix.length).replace(/_[0-9a-f]{12}$/, "");
	return OPERATIONS.has(operation) ? operation : null;
}
function visitTexts(value, into) {
	const item = object(value);
	if (!item) return;
	if (typeof item.text === "string") into.push(item.text);
	if (Array.isArray(item.content)) for (const child of item.content) visitTexts(child, into);
}
function runFromToolText(text) {
	if (!(text.includes("\"interaction_url\"") && text.includes("\"session_id\"")) && text.length > 8192 || text.length > 512 * 1024) return null;
	try {
		const parsed = JSON.parse(text);
		return presentationRun(parsed) ?? interaction({ structuredContent: parsed }) ?? interaction({ structuredContent: object(parsed)?.state ?? object(parsed)?.result });
	} catch {
		return null;
	}
}
/** DSH web logs MCP JSON in tool-result message text. Meta is optional and often absent. */
function eventRun(event, serverName) {
	const value = object(event);
	const data = object(value?.data);
	if (value?.type === "tool/result") {
		const fromMeta = presentationRun(data?.meta);
		if (fromMeta) return fromMeta;
		const texts = [];
		visitTexts(object(data?.message), texts);
		if (Array.isArray(data?.content)) for (const child of data.content) visitTexts(child, texts);
		for (const text of texts.reverse()) {
			const run = runFromToolText(text);
			if (run) return run;
		}
		return null;
	}
	if (value?.type !== "tool/ptc-dispatch" || data?.isError !== false || typeof data.name !== "string" || ![
		"start",
		"state",
		"step_begin",
		"step_claim",
		"step_resume",
		"step_complete"
	].includes(workflowOperation(data.name, serverName) ?? "") || !Array.isArray(data.content)) return null;
	for (const raw of [...data.content].reverse()) {
		const content = object(raw);
		if (typeof content?.text !== "string") continue;
		const run = runFromToolText(content.text);
		if (run) return run;
	}
	return null;
}

//#endregion
//#region src/host-adapter.ts
/** DSH SDK translation only. Workflow admission and delivery decisions live in the shared runtime. */
function dshRuntime(ctx, serverName) {
	let goals;
	ctx.inject(["goals"], (goalCtx) => {
		goals = goalCtx.goals;
		goalCtx.effect(() => () => {
			goals = void 0;
		});
	});
	function goalReason(runId, revision, stopped = false) {
		const run = createHash("sha256").update(runId).digest("hex").slice(0, 16);
		return `lazymind-${stopped ? "stopped" : "review"}-${run}-r${revision}`;
	}
	function inputSeq(events, requestId) {
		for (const event of events) {
			if (event.type !== "user/message") continue;
			if (object(object(object(event.data)?.message)?.source ?? object(event.data)?.source)?.rpcId === requestId) return event.seq;
		}
		return 0;
	}
	async function reconcile(sessionId, actionId, caller) {
		const controller = new AbortController();
		try {
			const frames = ctx.sessionController.follow({
				address: {
					kind: "session",
					sessionId
				},
				maxMessages: 100
			}, AbortSignal.any([caller, controller.signal]));
			for await (const frame of frames) {
				if (frame.type !== "snapshot") continue;
				const scan = (records$1) => inputSeq(records$1.flatMap((record) => record.type === "event" ? [record.event] : []), actionId);
				let found = scan(frame.records);
				let records = frame.records;
				let more = frame.hasMore;
				while (!found && more) {
					const seqs = records.map((record) => record.event.seq);
					if (!seqs.length) break;
					const page = await ctx.sessionController.page({
						address: {
							kind: "session",
							sessionId
						},
						throughSeq: frame.cursor,
						beforeSeq: Math.min(...seqs),
						maxMessages: 100
					}, AbortSignal.any([caller, controller.signal]));
					records = page.records;
					found = scan(records);
					more = page.hasMore;
				}
				return found;
			}
			return 0;
		} finally {
			controller.abort();
		}
	}
	return {
		id: (agent) => agent.session.id,
		parent: (agent) => ctx.agents.list().find((candidate) => candidate !== agent && ctx.agents.isOwnedBy(agent.session.id, candidate)),
		isLive: (agent) => ctx.agents.get(agent.session.id) === agent,
		isRunning: (agent) => agent.status === "running",
		history: (agent) => [...agent.session.ownEvents()].map((event) => ({
			seq: event.seq,
			time: event.time,
			user: event.type === "user/message",
			run: eventRun(event, serverName)
		})),
		canReturnResult: (agent) => !!ctx.tools.get("structured_output", agent),
		goal: (agent) => goals?.get(agent),
		suspendGoal(agent, input) {
			const goal = goals?.get(agent);
			if (goals && goal?.phase === "active" && goal.activation === "armed" && (!input.goalId || input.goalId === goal.id)) goals.block(agent, goal, {
				code: goalReason(input.runId, goal.revision + 1, input.continuation === "stopped"),
				message: input.continuation === "awaiting_executor" ? "LazyMind is executing this workflow step." : "This LazyMind workflow needs user action before automatic work can continue."
			});
		},
		resumeGoal(agent, runId) {
			const goal = goals?.get(agent);
			if (goals && goal?.phase === "blocked" && [goalReason(runId, goal.revision), goalReason(runId, goal.revision, true)].includes(goal.blockedReason?.code ?? "")) goals.resume(agent, goal);
		},
		async resolve(sessionId) {
			const result = await ctx.sessionController.resolveAgent(sessionId);
			return "error" in result ? { error: result.error.message } : { agent: result.agent };
		},
		async prompt(agent, input, signal) {
			await ctx.sessionController.prompt({
				sessionId: agent.session.id,
				requestId: input.actionId,
				mode: "queue",
				content: [{
					type: "text",
					text: input.message
				}]
			}, signal);
			return inputSeq(agent.session.snapshotEvents(), input.actionId);
		},
		cancel: (agent) => {
			ctx.sessionController.cancel({ sessionId: agent.session.id });
		},
		eventSeq: (agent) => Math.max(0, agent.session.seq - 1),
		reconcile,
		warn: (message) => ctx.logger.warn(message)
	};
}

//#endregion
//#region src/tool.ts
/** Preserve the MCP definition's output, cancellation and execution-local finalizer. */
function workflowTool(original, hooks) {
	return {
		...original,
		isConcurrencySafe: () => false,
		presentCall(args) {
			const fields = object(args);
			const rawInput = fields ? Object.fromEntries([
				"workflow_id",
				"session_id",
				"step_id",
				"execution_id"
			].filter((key) => typeof fields[key] === "string").map((key) => [key, fields[key]])) : void 0;
			return {
				card: "generic",
				title: `LazyMind Workflow: ${(hooks.operation ?? "operation").replaceAll("_", " ")}`,
				rawInput
			};
		},
		presentResult(args, result) {
			const meta = result.meta;
			return original.presentResult?.(args, {
				...result,
				meta: meta && typeof meta === "object" && !Array.isArray(meta) && "original" in meta ? meta.original : result.meta
			});
		},
		output: {
			...original.output,
			presentationMeta(args, value) {
				const previous = original.output.presentationMeta?.(args, value);
				const control = readControl(value);
				const run = interaction(value, hooks.trustedOrigin) ?? (control && hooks.trustedOrigin ? {
					runId: control.session_id,
					url: new URL(`/workflow-runs/${encodeURIComponent(control.session_id)}`, hooks.trustedOrigin).href
				} : null);
				const fields = object(object(value)?.structuredContent);
				const executionId = fields?.execution_id ?? object(fields?.execution)?.execution_id;
				const hostSessionId = hooks.hostSessionId;
				return run ? {
					lazymind_workflow: {
						...run,
						...hostSessionId ? { hostSessionId } : {},
						...hooks.operation ? { operation: hooks.operation } : {},
						...typeof executionId === "string" ? { executionId } : {}
					},
					original: previous ?? null
				} : previous ?? null;
			}
		},
		async execute(args, exec) {
			const value = await original.execute(args, exec);
			const control = hooks.afterResult ? await hooks.afterResult(value, exec) : readControl(value);
			if (hooks.shouldConclude ? hooks.shouldConclude(control, exec) : control?.continuation === "awaiting_user") exec.concludeTurn();
			const record = object(value);
			const fields = object(record?.structuredContent);
			const earlier = readControl(value);
			if (control && fields && record && (earlier?.state_version !== control.state_version || earlier?.continuation !== control.continuation)) return {
				...record,
				structuredContent: {
					...fields,
					control
				},
				...Array.isArray(record.content) ? { content: [...record.content, {
					type: "text",
					text: JSON.stringify({ control })
				}] } : {}
			};
			return value;
		}
	};
}

//#endregion
//#region src/host.ts
/** Wire DSH public hooks to the shared coordinator. MCP execution remains in workflowTool. */
function installHost(ctx, bridge, config, instanceId) {
	const lifetime = new AbortController();
	const runtime = dshRuntime(ctx, config.serverName);
	const coordinator = createCoordinator(runtime, bridge, config.webUrl, lifetime.signal);
	const registrations = /* @__PURE__ */ new Map();
	const tracked = /* @__PURE__ */ new Set();
	const own = (promise) => {
		tracked.add(promise);
		promise.then(() => tracked.delete(promise), () => tracked.delete(promise));
		return promise;
	};
	const call = (exec) => ({
		agent: exec.agent,
		operation: workflowOperation(exec.name, config.serverName),
		arguments: "arguments" in exec ? exec.arguments : void 0,
		returnsResult: exec.name === "structured_output",
		signal: exec.signal,
		callId: exec.callId,
		nested: !!exec.parent
	});
	function wrap(agent, registration) {
		const context = registration.context;
		if (!context) return;
		const live = /* @__PURE__ */ new Set();
		for (const schema of ctx.tools.schemas()) {
			const operation = workflowOperation(schema.name, config.serverName);
			if (operation === null) continue;
			live.add(schema.name);
			const original = ctx.tools.get(schema.name);
			const previous = registration.wrappers.get(schema.name);
			if (!original || previous?.original === original) continue;
			previous?.dispose();
			const definition = workflowTool(original, {
				trustedOrigin: config.webUrl,
				hostSessionId: runtime.id(coordinator.driver(agent)),
				operation,
				afterResult: (value, exec) => own(coordinator.afterResult(value, call(exec))),
				shouldConclude: (control) => coordinator.shouldConclude(agent, control)
			});
			registration.wrappers.set(schema.name, {
				original,
				dispose: context.tools.register(definition)
			});
		}
		for (const [name, value] of registration.wrappers) if (!live.has(name)) {
			value.dispose();
			registration.wrappers.delete(name);
		}
	}
	function ensure(agent) {
		const existing = registrations.get(agent);
		if (existing) return existing;
		coordinator.ensure(agent);
		const entry = {
			ready: Promise.resolve(),
			dispose: async () => {},
			disposeGuard: () => {},
			wrappers: /* @__PURE__ */ new Map()
		};
		registrations.set(agent, entry);
		const registration = agent.ctx.inject(["tools"], (injected) => {
			entry.context = injected;
			entry.disposeGuard = injected.tools.guard((exec) => coordinator.denial(exec.agent ?? agent, call(exec)));
			wrap(agent, entry);
		});
		entry.ready = registration.await();
		entry.dispose = () => registration.dispose();
		return entry;
	}
	ctx.on("agent/pre-step", (payload, next) => own((async () => {
		const registration = ensure(payload.agent);
		await registration.ready;
		wrap(payload.agent, registration);
		return await coordinator.beforeTurn({
			...payload,
			messages: payload.messages.map((message) => {
				const source = object(object(message)?.source);
				return {
					user: source?.kind === "user",
					requestId: typeof source?.rpcId === "string" ? source.rpcId : void 0
				};
			})
		}) ? next() : { kind: "reject" };
	})()));
	ctx.on("tools/pre-execute", (exec, next) => own((async () => {
		if (exec.agent) await ensure(exec.agent).ready;
		const reason = await coordinator.beforeTool(call(exec));
		return reason ? {
			kind: "deny",
			reason
		} : next();
	})()));
	ctx.on("tools/ptc-dispatch-log", async (dispatch, next) => {
		const content = await next();
		const run = coordinator.takeNestedLink(dispatch.subCallId);
		return run ? [...content, {
			type: "text",
			text: JSON.stringify({ lazymind_workflow: run })
		}] : content;
	});
	ctx.on("agent/status", ({ agent, status }) => {
		if (status === "idle") coordinator.idle(agent);
	});
	ctx.on("agent/disposed", ({ agent }) => {
		const entry = registrations.get(agent);
		if (entry) {
			entry.disposeGuard();
			for (const wrapper of entry.wrappers.values()) wrapper.dispose();
			registrations.delete(agent);
			own(entry.dispose());
		}
		coordinator.forget(agent);
	});
	for (const agent of ctx.agents.list()) ensure(agent);
	const polling = createDispatcher(runtime, coordinator, bridge, instanceId, lifetime.signal).poll();
	return async () => {
		lifetime.abort();
		await polling;
		await Promise.allSettled([...tracked]);
		for (const entry of registrations.values()) {
			entry.disposeGuard();
			for (const wrapper of entry.wrappers.values()) wrapper.dispose();
			await entry.dispose();
		}
		registrations.clear();
		coordinator.dispose();
	};
}

//#endregion
//#region src/runtime-lock.ts
/** One cooperating dispatcher per pairing/profile, including across DSH processes. */
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
		if (!Number.isSafeInteger(previous.pid) || previous.pid < 1) throw new Error("Invalid workflow dispatcher lock; repair this connection explicitly");
		try {
			process.kill(previous.pid, 0);
		} catch (error$1) {
			if (error$1.code === "ESRCH") {
				await unlink(path);
				continue;
			}
			throw error$1;
		}
		throw new Error("This DSH profile already has a workflow dispatcher; close its previous DSH process");
	}
	throw new Error("Could not acquire the workflow dispatcher lock");
}

//#endregion
//#region src/index.ts
const inject = [
	"tools",
	"agents",
	"sessionController"
];
/** Install only public DSH extensions. The pairing secret stays in a private host file. */
async function apply(ctx, config) {
	if (!config.webUrl || !config.serverName) throw new Error("Reconnect DSH from LazyMind to configure the workflow bundle");
	const pairing = await loadPairing(config.pairingFile);
	const instance = randomUUID();
	const release = await dispatcherLock(config.pairingFile, instance);
	try {
		const dispose = installHost(ctx, new HostBridge(config.bridgeUrl, pairing), config, instance);
		ctx.effect(() => async () => {
			try {
				await dispose();
			} finally {
				await release();
			}
		});
	} catch (error) {
		await release();
		throw error;
	}
}

//#endregion
export { apply, inject };