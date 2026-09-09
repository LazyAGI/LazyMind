import { createHash, randomUUID } from "node:crypto";
import { open, readFile, stat, unlink } from "node:fs/promises";
import { setTimeout } from "node:timers/promises";

//#region src/protocol.ts
function object(value) {
	return typeof value === "object" && value !== null && !Array.isArray(value) ? value : null;
}
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
	"step_submit",
	"artifact_list",
	"artifact_get"
]);
function workflowOperation(name, serverName) {
	const prefix = `mcp__${serverName}__workflow_`;
	if (!name.startsWith(prefix)) return null;
	const operation = name.slice(prefix.length).replace(/_[0-9a-f]{12}$/, "");
	return OPERATIONS.has(operation) ? operation : null;
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
/** Read only our standard-event projection, including PTC's standard nested dispatch log. */
function eventRun(event, serverName) {
	const value = object(event);
	const data = object(value?.data);
	if (value?.type === "tool/result") return presentationRun(data?.meta);
	if (value?.type !== "tool/code-dispatch" || data?.isError !== false || typeof data.name !== "string" || ![
		"start",
		"state",
		"step_begin",
		"step_claim",
		"step_resume",
		"step_submit"
	].includes(workflowOperation(data.name, serverName) ?? "") || !Array.isArray(data.content)) return null;
	for (const raw of [...data.content].reverse()) {
		const content = object(raw);
		if (content?.type !== "text" || typeof content.text !== "string" || content.text.length > 8192) continue;
		try {
			const run = presentationRun(JSON.parse(content.text));
			if (run) return run;
		} catch {}
	}
	return null;
}

//#endregion
//#region src/bridge.ts
var BridgeError = class extends Error {
	constructor(code, message, status) {
		super(message);
		this.code = code;
		this.status = status;
	}
};
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
const STRUCTURED_OUTPUT = "structured_output";
/** DSH-specific lifecycle work stays here; business decisions stay in Core. */
function installHost(ctx, bridge, config, instanceId) {
	const lifetime = new AbortController();
	const scopes = /* @__PURE__ */ new Map();
	const tracked = /* @__PURE__ */ new Set();
	const ptcLinks = /* @__PURE__ */ new Map();
	const actionCache = /* @__PURE__ */ new Map();
	let goals;
	ctx.inject(["goals"], (goalCtx) => {
		goals = goalCtx.goals;
		goalCtx.effect(() => () => {
			goals = void 0;
		});
	});
	const own = (promise) => {
		tracked.add(promise);
		promise.then(() => tracked.delete(promise), () => tracked.delete(promise));
		return promise;
	};
	const signal = (caller) => caller ? AbortSignal.any([caller, lifetime.signal]) : lifetime.signal;
	const driver = (agent) => {
		const visited = /* @__PURE__ */ new Set();
		while (!visited.has(agent)) {
			visited.add(agent);
			const parent = ctx.agents.list().find((candidate) => candidate !== agent && ctx.agents.isOwnedBy(agent.session.id, candidate));
			if (!parent) return agent;
			agent = parent;
		}
		throw new Error("Workflow driver ownership contains a cycle");
	};
	const paused = (scope) => scope.unknown || !!scope.control && PAUSED.has(scope.control.continuation);
	const completion = (scope) => driver(scope.agent) !== scope.agent && scope.returnPending && !!ctx.tools.get(STRUCTURED_OUTPUT, scope.agent);
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
	function goalReason(runId, revision, stopped = false) {
		const run = createHash("sha256").update(runId).digest("hex").slice(0, 16);
		return `lazymind-${stopped ? "stopped" : "review"}-${run}-r${revision}`;
	}
	function suspendGoal(scope) {
		if (!goals || !scope.runId || !scope.automatic || driver(scope.agent) !== scope.agent || !scope.control || !PAUSED.has(scope.control.continuation)) return;
		const goal = goals.get(scope.agent);
		if (goal?.phase === "active" && goal.activation === "armed" && (!scope.goalId || scope.goalId === goal.id)) goals.block(scope.agent, goal, {
			code: goalReason(scope.runId, goal.revision + 1, scope.control?.continuation === "stopped"),
			message: scope.control.continuation === "awaiting_executor" ? "LazyMind is executing this workflow step." : "This LazyMind workflow needs user action before automatic work can continue."
		});
	}
	function resumeGoal(scope) {
		if (!goals || !scope.runId) return;
		const goal = goals.get(scope.agent);
		if (goal?.phase === "blocked" && [goalReason(scope.runId, goal.revision), goalReason(scope.runId, goal.revision, true)].includes(goal.blockedReason?.code ?? "")) goals.resume(scope.agent, goal);
	}
	function remember(scope) {
		const root = driver(scope.agent);
		const finished = /* @__PURE__ */ new Set();
		let ownedAt = 0;
		let ownedSeq = -1;
		let latestInputSeq = -1;
		for (const event of [...scope.agent.session.ownEvents()].reverse()) {
			if (event.type === "user/message" && latestInputSeq < 0) latestInputSeq = event.seq;
			const run = eventRun(event, config.serverName);
			if (!run || run.hostSessionId !== root.session.id || !run.operation || ![
				"start",
				"step_begin",
				"step_claim",
				"step_resume",
				"step_submit"
			].includes(run.operation)) continue;
			if (!scope.runId) {
				scope.runId = run.runId;
				scope.automatic = true;
				ownedAt = event.time;
				ownedSeq = event.seq;
			}
			if (run.runId !== scope.runId || !run.executionId) continue;
			if (run.operation === "step_submit") finished.add(run.executionId);
			else if (ACQUIRE.has(run.operation) && !finished.has(run.executionId)) scope.grants.add(run.executionId);
		}
		if (latestInputSeq > ownedSeq) scope.automatic = false;
		scope.activeOwned = scope.agent.status === "running" && scope.automatic;
		if (!scope.runId && root !== scope.agent) scope.runId = ensure(root).runId;
		const goal = goals?.get(root);
		if (goal && ownedAt && goal.createdAt <= ownedAt) scope.goalId = goal.id;
		else if (goal && ownedAt && goal.createdAt > ownedAt) scope.automatic = false;
	}
	function denial(scope, exec) {
		const operation = workflowOperation(exec.name, config.serverName);
		if (operation && (READS.has(operation) || operation === "session_stop")) return void 0;
		if (!scope.runId) return void 0;
		if (exec.name === STRUCTURED_OUTPUT && completion(scope)) return void 0;
		if (scope.unknown && (scope.activeOwned || operation)) return "Workflow state is unavailable; retry after reconnecting LazyMind.";
		if (!paused(scope)) return void 0;
		if (scope.control?.continuation === "stopped") return operation || scope.activeOwned ? "This Workflow has been stopped." : void 0;
		if (operation === "step_submit" || operation === "step_resume" || operation === "step_claim") {
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
			ready: Promise.resolve(),
			disposeRegistration: async () => {},
			unknown: false,
			grants: /* @__PURE__ */ new Set(),
			manual: false,
			activeOwned: false,
			automatic: false,
			returnPending: false,
			wrappers: /* @__PURE__ */ new Map(),
			disposeGuard: () => {}
		};
		scopes.set(agent, scope);
		remember(scope);
		const registration = agent.ctx.inject(["tools"], (injected) => {
			scope.registration = injected;
			scope.disposeGuard = injected.tools.guard((exec) => denial(exec.agent ? scopes.get(exec.agent) ?? scope : scope, exec));
			wrap(scope);
		});
		scope.ready = registration.await();
		scope.disposeRegistration = () => registration.dispose();
		return scope;
	}
	async function refresh(scope, caller) {
		if (!scope.runId) return void 0;
		try {
			const state = await bridge.state(scope.runId, signal(caller));
			if (state.binding?.driver_session_id && state.binding.driver_session_id !== driver(scope.agent).session.id) {
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
		if (!exec.agent || ctx.agents.get(exec.agent.session.id) !== exec.agent || lifetime.signal.aborted) return null;
		const scope = ensure(exec.agent);
		const root = driver(exec.agent);
		const rootScope = ensure(root);
		const operation = workflowOperation(exec.name, config.serverName);
		const returned = readControl(value);
		const fields = object(object(value)?.structuredContent);
		const run = interaction(value, config.webUrl) ?? (returned ? {
			runId: returned.session_id,
			url: new URL(`/workflow-runs/${encodeURIComponent(returned.session_id)}`, config.webUrl).href
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
		if (operation === "step_submit" && typeof fields?.execution_id === "string") {
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
			const fresh = operation === "start" ? await bridge.bind(runId, root.session.id, signal(exec.signal)) : returned ?? await bridge.state(runId, signal(exec.signal));
			if (fresh.binding?.driver_session_id !== root.session.id) return null;
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
				rootScope.goalId = goals?.get(root)?.id;
			}
			publish(runId, fresh);
			suspendGoal(rootScope);
			if (exec.parent && run) ptcLinks.set(exec.callId, {
				...run,
				hostSessionId: root.session.id,
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
			ctx.logger.warn(`lazymind-workflow: result committed, control synchronization failed: ${String(error)}`);
			return null;
		}
	}
	function wrap(scope) {
		const registration = scope.registration;
		if (!registration) return;
		const live = /* @__PURE__ */ new Set();
		for (const schema of ctx.tools.schemas()) {
			const operation = workflowOperation(schema.name, config.serverName);
			if (operation === null) continue;
			live.add(schema.name);
			const original = ctx.tools.get(schema.name);
			const previous = scope.wrappers.get(schema.name);
			if (!original || previous?.original === original) continue;
			previous?.dispose();
			const definition = workflowTool(original, {
				trustedOrigin: config.webUrl,
				hostSessionId: driver(scope.agent).session.id,
				operation,
				afterResult: (value, exec) => own(afterResult(value, exec)),
				shouldConclude: (control) => scope.activeOwned && !completion(scope) && (scope.unknown || !!scope.runId && ["awaiting_user", "awaiting_executor"].includes(control?.continuation ?? ""))
			});
			scope.wrappers.set(schema.name, {
				original,
				dispose: registration.tools.register(definition)
			});
		}
		for (const [name, value] of scope.wrappers) if (!live.has(name)) {
			value.dispose();
			scope.wrappers.delete(name);
		}
	}
	async function lookupInput(agent, messages, caller) {
		for (const message of messages) {
			const source = object(object(message)?.source);
			if (source?.kind !== "user" || typeof source.rpcId !== "string") continue;
			let claim = actionCache.get(source.rpcId);
			try {
				claim = await bridge.action(source.rpcId, signal(caller));
			} catch (error) {
				if (error instanceof BridgeError && (error.status === 404 || error.status === 403)) continue;
				if (error instanceof BridgeError && error.code === "BINDING_STALE") throw error;
				if (!claim) continue;
			}
			if (claim && claim.action.native_session_id === agent.session.id) {
				actionCache.set(source.rpcId, claim);
				return claim;
			}
		}
	}
	ctx.on("agent/pre-step", (payload, next) => own((async () => {
		const scope = ensure(payload.agent);
		await scope.ready;
		wrap(scope);
		if (scope.turn !== payload.turn) {
			scope.turn = payload.turn;
			scope.manual = false;
			scope.activeOwned = scope.automatic;
			scope.actionId = void 0;
		}
		try {
			const action = await lookupInput(payload.agent, payload.messages, payload.signal);
			if (action) {
				if (action.action.status === "superseded" || action.action.consumed_at && scope.actionId !== action.action.id) return { kind: "reject" };
				scope.runId = action.action.session_id;
				scope.actionId = action.action.id;
				scope.activeOwned = scope.automatic = true;
				scope.manual = false;
				publish(scope.runId, action.control);
			} else if (payload.messages.some((message) => object(object(message)?.source)?.kind === "user")) {
				scope.manual = true;
				scope.activeOwned = scope.automatic = false;
			}
			if ((!scope.manual || scope.activeOwned) && !completion(scope)) await refresh(scope, payload.signal);
			suspendGoal(ensure(driver(scope.agent)));
			if (scope.runId && scope.activeOwned && paused(scope) && !scope.manual && scope.grants.size === 0 && !completion(scope)) return { kind: "reject" };
			return next();
		} catch (error) {
			ctx.logger.warn(`lazymind-workflow: control gate deferred a step: ${String(error)}`);
			return { kind: "reject" };
		}
	})()));
	ctx.on("tools/pre-execute", (exec, next) => own((async () => {
		if (!exec.agent) return next();
		const scope = ensure(exec.agent);
		await scope.ready;
		if (exec.name === STRUCTURED_OUTPUT && completion(scope)) return next();
		const operation = workflowOperation(exec.name, config.serverName);
		const runId = object(exec.arguments)?.session_id;
		if (operation && !READS.has(operation) && typeof runId === "string" && runId !== scope.runId) try {
			const fresh = await bridge.state(runId, signal(exec.signal));
			if (fresh.binding?.driver_session_id !== driver(exec.agent).session.id) return {
				kind: "deny",
				reason: "This Workflow belongs to another driver session."
			};
			scope.runId = runId;
			publish(runId, fresh);
		} catch (error) {
			return {
				kind: "deny",
				reason: `Workflow state unavailable: ${String(error)}`
			};
		}
		if (scope.runId && (scope.activeOwned || operation && !READS.has(operation))) try {
			await refresh(scope, exec.signal);
		} catch {
			return {
				kind: "deny",
				reason: "Workflow state is unavailable; reconnect LazyMind."
			};
		}
		const reason = denial(scope, exec);
		return reason ? {
			kind: "deny",
			reason
		} : next();
	})()));
	ctx.on("tools/ptc-dispatch-log", async (dispatch, next) => {
		const content = await next();
		const run = ptcLinks.get(dispatch.subCallId);
		ptcLinks.delete(dispatch.subCallId);
		return run ? [...content, {
			type: "text",
			text: JSON.stringify({ lazymind_workflow: run })
		}] : content;
	});
	ctx.on("agent/status", ({ agent, status }) => {
		if (status === "idle") {
			const scope = scopes.get(agent);
			if (scope) {
				scope.activeOwned = false;
				scope.manual = false;
			}
		}
	});
	ctx.on("agent/disposed", ({ agent }) => {
		const scope = scopes.get(agent);
		if (!scope) return;
		scope.disposeGuard();
		for (const wrapper of scope.wrappers.values()) wrapper.dispose();
		scopes.delete(agent);
		own(scope.disposeRegistration());
	});
	for (const agent of ctx.agents.list()) ensure(agent);
	function inputSeq(events, requestId) {
		for (const event of events) {
			if (event.type !== "user/message") continue;
			if (object(object(object(event.data)?.message)?.source ?? object(event.data)?.source)?.rpcId === requestId) return event.seq;
		}
		return 0;
	}
	async function reconcile(action) {
		const controller = new AbortController();
		try {
			const frames = ctx.sessionController.follow({
				address: {
					kind: "session",
					sessionId: action.native_session_id
				},
				maxMessages: 100
			}, signal(controller.signal));
			for await (const frame of frames) {
				if (frame.type !== "snapshot") continue;
				const scan = (records$1) => inputSeq(records$1.flatMap((record) => record.type === "event" ? [record.event] : []), action.id);
				let found = scan(frame.records);
				let records = frame.records;
				let more = frame.hasMore;
				while (!found && more) {
					const seqs = records.map((record) => record.event.seq);
					if (!seqs.length) break;
					const page = await ctx.sessionController.page({
						address: {
							kind: "session",
							sessionId: action.native_session_id
						},
						throughSeq: frame.cursor,
						beforeSeq: Math.min(...seqs),
						maxMessages: 100
					}, signal(controller.signal));
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
	async function deliver(action) {
		if (action.kind === "cancel" && action.status !== "pending") {
			const resolved$1 = await ctx.sessionController.resolveAgent(action.native_session_id);
			if ("error" in resolved$1) return;
			const scope$1 = ensure(resolved$1.agent);
			if (scope$1.runId === action.session_id && (scope$1.activeOwned || scope$1.grants.size > 0)) ctx.sessionController.cancel({ sessionId: resolved$1.agent.session.id });
			if (scope$1.runId === action.session_id) suspendGoal(scope$1);
			const seq = Math.max(0, resolved$1.agent.session.seq - 1);
			if (seq > 0) await bridge.settle(action.id, instanceId, "", "accepted", seq, "", signal());
			return;
		}
		if (action.kind === "continue" && action.status !== "pending") {
			const seq = await reconcile(action);
			if (seq > 0) {
				await bridge.settle(action.id, instanceId, "", "accepted", seq, "", signal());
				return;
			}
		}
		const claim = await bridge.claim(action.id, instanceId, signal());
		actionCache.set(action.id, claim);
		if (!claim.dispatch_token || claim.action.status !== "dispatching") return;
		const resolved = await ctx.sessionController.resolveAgent(action.native_session_id);
		if ("error" in resolved) {
			await bridge.settle(action.id, instanceId, claim.dispatch_token, "failed", 0, resolved.error.message, signal());
			return;
		}
		const scope = ensure(resolved.agent);
		if (scope.runId === action.session_id) publish(action.session_id, claim.control);
		const current = await bridge.action(action.id, signal());
		if (current.action.consumed_at || current.control.binding?.generation !== action.binding_generation) return;
		try {
			if (action.kind === "cancel") {
				if (scope.runId === action.session_id && (scope.activeOwned || scope.grants.size > 0)) ctx.sessionController.cancel({ sessionId: resolved.agent.session.id });
				if (scope.runId === action.session_id) suspendGoal(scope);
			} else {
				if (!action.execution_id && scope.runId === action.session_id && scope.activeOwned && scope.grants.size === 0) ctx.sessionController.cancel({ sessionId: resolved.agent.session.id });
				await ctx.sessionController.prompt({
					sessionId: resolved.agent.session.id,
					requestId: action.id,
					mode: "queue",
					content: [{
						type: "text",
						text: action.execution_id ? `LazyMind workflow ${action.session_id} has an execution update. Call workflow.step.claim with execution_id=${action.execution_id} to inspect or acquire it. If executor_host is lazymind, it is managed by LazyMind: read the latest state and continue only ready steps when permitted; do not execute or submit it. Otherwise execute the granted contract and submit with execution_handle. Do not create a new workflow.` : `The user clicked Continue in the LazyMind panel for workflow ${action.session_id} and has finished the current review. Call workflow.state, then workflow.step.begin for a ready step when control.continuation=continue and admission.can_begin=true. A human step requires review AFTER execution; its mode or requires_approval flag does not require another confirmation before begin. Continue until awaiting_user, awaiting_executor, stopped, or completed. For executor_host=lazymind, let LazyMind execute its tools; otherwise submit using execution_handle. Do not ask the user to confirm the review again or create a new workflow.`
					}]
				}, signal());
				if (scope.runId === action.session_id) resumeGoal(scope);
			}
			const seq = action.kind === "continue" ? inputSeq(resolved.agent.session.snapshotEvents(), action.id) : Math.max(0, resolved.agent.session.seq - 1);
			await bridge.settle(action.id, instanceId, claim.dispatch_token, "accepted", seq, "", signal());
		} catch (error) {
			await bridge.settle(action.id, instanceId, claim.dispatch_token, "unknown", 0, String(error), signal());
		}
	}
	const polling = (async () => {
		while (!lifetime.signal.aborted) {
			try {
				let after = "";
				do {
					const page = await bridge.actions(after, signal());
					for (const action of page.actions) try {
						await deliver(action);
					} catch (error) {
						if (!(error instanceof BridgeError && [
							"DELIVERY_PENDING",
							"ACTION_CONSUMED",
							"BINDING_STALE",
							"WORKFLOW_ADMISSION_DENIED"
						].includes(error.code)) && !lifetime.signal.aborted) ctx.logger.warn(`lazymind-workflow: delivery pending: ${String(error)}`);
					}
					after = page.next_page_token ?? "";
				} while (after && !lifetime.signal.aborted);
			} catch (error) {
				if (!lifetime.signal.aborted) ctx.logger.warn(`lazymind-workflow: reconnecting Bridge: ${String(error)}`);
			}
			try {
				await setTimeout(1e3, void 0, { signal: lifetime.signal });
			} catch {
				break;
			}
		}
	})();
	return async () => {
		lifetime.abort();
		await polling;
		await Promise.allSettled([...tracked]);
		for (const scope of scopes.values()) {
			scope.disposeGuard();
			for (const wrapper of scope.wrappers.values()) wrapper.dispose();
			await scope.disposeRegistration();
		}
		scopes.clear();
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