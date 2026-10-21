import { open, readFile, stat, unlink } from "node:fs/promises";
import { homedir } from "node:os";
import { isAbsolute, join, resolve } from "node:path";
import { parseArgs, promisify } from "node:util";
import { randomUUID } from "node:crypto";
import { execFile } from "node:child_process";
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
					for (const action of page.actions) try {
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
function continuationMessage(action) {
	const request = action.execution_id ? `When applicable, call workflow.step.claim with execution_id=${action.execution_id}. For a completed internal execution, inspect its result and the current state instead of executing it again.` : "When control.continuation=continue and admission.can_begin=true, call workflow.step.begin for a ready step.";
	return `LazyMind workflow ${action.session_id} has a control notification. Call workflow.state first; the notification may be delayed. ${request} Execute only a granted external step_contract and publish/submit using its execution_handle. If executor_host is lazymind, let Core execute it. After submission, use the returned state to continue. Yield when awaiting_user, awaiting_executor, draining, stopped, binding_required, completed, or failed; do not poll while waiting. A human step requires review AFTER execution. Do not assume this notification means a review was approved. Do not create a new workflow.`;
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
	cancellation = "none";
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
		console.log(`Codex Workflow queue dispatcher ready. MCP pairing file: ${path}. Native turn interruption is unavailable.`);
		await createDispatcher(adapter, void 0, bridge, instanceId, lifetime.signal).poll();
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