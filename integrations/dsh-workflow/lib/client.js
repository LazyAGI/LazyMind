window.__ModuleLoader__.load({ id: "@lazymind/dsh-workflow", factory: (require) => {
var module = { exports: {} }; var exports = module.exports;
let react = require("react");
let react_dom = require("react-dom");
let react_jsx_runtime = require("react/jsx-runtime");

//#region ../workflow-agent-core/src/protocol.ts
const WORKFLOW_OPERATIONS = new Set([
	"list",
	"get",
	"input_import",
	"input_get",
	"input_answer",
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
	"artifact_get",
	"tool_list",
	"tool_call",
	"generation_claim",
	"generation_respond"
]);
function workflowOperationName(value) {
	return WORKFLOW_OPERATIONS.has(value) ? value : null;
}
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
//#region src/events.ts
function presentationLink(runId, webUrl) {
	if (typeof runId !== "string" || !/^[a-zA-Z0-9_-]{1,256}$/.test(runId)) return null;
	try {
		const base = new URL(webUrl);
		if (!["http:", "https:"].includes(base.protocol) || base.username || base.password || base.search || base.hash) return null;
		return {
			runId,
			url: new URL(`workflow-runs/${encodeURIComponent(runId)}`, base.href.replace(/\/?$/, "/")).href
		};
	} catch {
		return null;
	}
}
function presentationRun(meta, webUrl) {
	const value = object(object(meta)?.lazymind_workflow);
	const run = presentationLink(value?.runId, webUrl);
	if (!run || !value) return null;
	return {
		...run,
		...typeof value.hostSessionId === "string" ? { hostSessionId: value.hostSessionId } : {},
		...typeof value.operation === "string" ? { operation: value.operation } : {},
		...typeof value.executionId === "string" ? { executionId: value.executionId } : {},
		...typeof value.modelJobId === "string" ? { modelJobId: value.modelJobId } : {},
		...typeof value.modelJobStatus === "string" ? { modelJobStatus: value.modelJobStatus } : {}
	};
}
function workflowOperation(name, serverName) {
	const prefix = `mcp__${serverName}__workflow_`;
	if (!name.startsWith(prefix)) return null;
	return workflowOperationName(name.slice(prefix.length).replace(/_[0-9a-f]{12}$/, ""));
}
function visitTexts(value, into) {
	const item = object(value);
	if (!item) return;
	if (typeof item.text === "string") into.push(item.text);
	if (Array.isArray(item.content)) for (const child of item.content) visitTexts(child, into);
}
function runFromToolText(text, webUrl) {
	if (!(text.includes("\"session_id\"") || text.includes("\"lazymind_workflow\"")) && text.length > 8192 || text.length > 512 * 1024) return null;
	try {
		const parsed = JSON.parse(text);
		const value = { structuredContent: object(parsed)?.result ?? parsed };
		const control = readControl(value);
		return presentationRun(parsed, webUrl) ?? presentationLink(control?.session_id, webUrl) ?? interaction(value, webUrl);
	} catch {
		return null;
	}
}
/** DSH web logs MCP JSON in tool-result message text. Meta is optional and often absent. */
function eventRun(event, serverName, webUrl) {
	if (!presentationLink("config-check", webUrl)) return null;
	const value = object(event);
	const data = object(value?.data);
	if (value?.type === "tool/result") {
		const fromMeta = presentationRun(data?.meta, webUrl);
		if (fromMeta) return fromMeta;
		const texts = [];
		visitTexts(object(data?.message), texts);
		if (Array.isArray(data?.content)) for (const child of data.content) visitTexts(child, texts);
		for (const text of texts.reverse()) {
			const run = runFromToolText(text, webUrl);
			if (run) return run;
		}
		return null;
	}
	if (value?.type !== "tool/ptc-dispatch" || data?.isError !== false || typeof data.name !== "string" || ![
		"start",
		"state",
		"input_answer",
		"step_begin",
		"step_claim",
		"step_resume",
		"step_complete",
		"generation_claim",
		"generation_respond"
	].includes(workflowOperation(data.name, serverName) ?? "") || !Array.isArray(data.content)) return null;
	for (const raw of [...data.content].reverse()) {
		const content = object(raw);
		if (typeof content?.text !== "string") continue;
		const run = runFromToolText(content.text, webUrl);
		if (run) return run;
	}
	return null;
}

//#endregion
//#region ../workflow-agent-core/src/panel-store.ts
function runKey(run) {
	return `${run.hostSessionId}\0${new URL(run.url).origin}\0${run.runId}`;
}
/** Per-plugin presentation state. It never binds, confirms, stops or resumes a workflow. */
function windowStore() {
	let state = {
		entries: {},
		firstCards: {}
	};
	const listeners = /* @__PURE__ */ new Set();
	const publish = (next) => {
		state = next;
		for (const listener of listeners) listener();
	};
	const update = (id, entry$1) => publish({
		...state,
		entries: {
			...state.entries,
			[id]: entry$1
		}
	});
	const entry = (sessionId) => Object.hasOwn(state.entries, sessionId) ? state.entries[sessionId] : void 0;
	const close = (sessionId) => {
		if (!entry(sessionId)) return;
		const entries = { ...state.entries };
		delete entries[sessionId];
		publish({
			...state,
			entries
		});
	};
	return {
		snapshot: () => state,
		subscribe(listener) {
			listeners.add(listener);
			return () => {
				listeners.delete(listener);
			};
		},
		observe(run, anchor) {
			if (!run.hostSessionId) return;
			const key = runKey(run);
			const first = state.firstCards[key];
			if (first === void 0 || anchor < first) publish({
				...state,
				firstCards: {
					...state.firstCards,
					[key]: anchor
				}
			});
		},
		open(run) {
			if (run.hostSessionId) update(run.hostSessionId, {
				run,
				minimized: false,
				source: "manual"
			});
		},
		binding(sessionId, run) {
			const current = entry(sessionId);
			if (current?.source === "manual") return;
			if (!run) {
				close(sessionId);
				return;
			}
			if (current && runKey(current.run) === runKey(run)) return;
			update(sessionId, {
				run,
				minimized: false,
				source: "binding"
			});
		},
		close,
		clear() {
			if (Object.keys(state.entries).length > 0) publish({
				...state,
				entries: {}
			});
		},
		minimize(id) {
			const current = entry(id);
			if (current) update(id, {
				...current,
				minimized: true
			});
		},
		dispose() {
			listeners.clear();
			state = {
				entries: {},
				firstCards: {}
			};
		}
	};
}

//#endregion
//#region src/presentation-config.ts
const PRESENTATION_CONFIG_PATH = "/lazymind-workflow/config";
const CONFIG_KEY = "__LAZYMIND_WORKFLOW_PRESENTATION__";
function parsePresentationConfig(input) {
	const value = object(input);
	if (typeof value?.serverName !== "string" || !value.serverName || typeof value.webUrl !== "string" || !presentationLink("config-check", value.webUrl)) throw new Error("Reconnect LazyMind Workflow and reload DSH to load its panel configuration");
	return {
		serverName: value.serverName,
		webUrl: value.webUrl
	};
}
function readPresentationConfig(scope) {
	return parsePresentationConfig(object(scope)?.[CONFIG_KEY]);
}
/** Optional connections must not prevent the rest of the DSH client from booting. */
function observePresentationConfig(scope, update, request = fetch, interval = 3e3) {
	const lifetime = new AbortController();
	let timer;
	try {
		update(readPresentationConfig(scope));
	} catch {
		update(void 0);
	}
	const poll = async () => {
		try {
			const response = await request(PRESENTATION_CONFIG_PATH, {
				signal: lifetime.signal,
				cache: "no-store"
			});
			if (response.ok) {
				const { config } = await response.json();
				if (!lifetime.signal.aborted) if (config === null) update(void 0);
				else update(readPresentationConfig({ [CONFIG_KEY]: config }));
			}
		} catch {}
		if (!lifetime.signal.aborted) timer = setTimeout(() => {
			poll();
		}, interval);
	};
	if (typeof location !== "undefined" && /^https?:$/.test(location.protocol)) poll();
	return () => {
		lifetime.abort();
		clearTimeout(timer);
	};
}

//#endregion
//#region ../workflow-agent-core/src/presentation.ts
const bindingRefreshMs = 5e3;
const layoutRetryMs = 100;
function delay(ms, signal) {
	signal.throwIfAborted();
	return new Promise((resolve, reject) => {
		const timer = setTimeout(() => {
			signal.removeEventListener("abort", stop);
			resolve();
		}, ms);
		function stop() {
			clearTimeout(timer);
			reject(signal.reason);
		}
		signal.addEventListener("abort", stop, { once: true });
	});
}
/** The caller owns the window and its renderer cleanup. */
async function runPresentation({ watchView, lookupBinding, ui, frameUrl, signal, report }) {
	let current;
	async function synchronize(view, clear, active) {
		function publish(status, runId, pageStatus) {
			if (!active.aborted) report({
				status,
				checked_at: Date.now(),
				native_session_id: view.chatId ?? void 0,
				run_id: runId,
				page_status: pageStatus
			});
		}
		async function apply$1(target, statusForUnmount, retryUntil = 0) {
			do {
				if (active.aborted) return;
				try {
					const result = await ui.syncPanel(view, target, active);
					if (result.status !== "stale") publish(result.issue ?? (result.status === "unmounted" ? statusForUnmount ?? "unmounted" : result.status === "reused" ? "mounted" : result.status), target?.runId, result.pageStatus);
					if (result.status !== "layout-unavailable" || result.issue) return;
				} catch {
					publish("renderer-unavailable", target?.runId);
					return;
				}
				if (Date.now() >= retryUntil || active.aborted) return;
				await delay(layoutRetryMs, active);
			} while (!active.aborted);
		}
		try {
			if (clear || view.chatId === null) await apply$1(null, view.chatId === null ? "waiting-session" : void 0);
			if (view.chatId === null) return;
			while (!active.aborted) {
				let binding;
				try {
					binding = await lookupBinding(view.chatId, active);
				} catch {
					binding = { status: "unavailable" };
				}
				if (active.aborted) return;
				const refreshAt = Math.max(Date.now() + bindingRefreshMs, "retryAt" in binding ? binding.retryAt ?? 0 : 0);
				if (binding.status === "bound") await apply$1({
					runId: binding.runId,
					frameUrl
				}, void 0, refreshAt);
				else if (binding.status === "unavailable") publish("binding-unavailable");
				else await apply$1(null, binding.status);
				if (!active.aborted) await delay(Math.max(0, refreshAt - Date.now()), active);
			}
		} catch (error) {
			if (!active.aborted) throw error;
		}
	}
	try {
		await watchView((view) => {
			if (signal.aborted || current?.view.epoch === view.epoch && current.view.chatId === view.chatId) return;
			const clear = current !== void 0;
			current?.stop.abort();
			const stop = new AbortController();
			current = {
				view,
				stop
			};
			const active = AbortSignal.any([signal, stop.signal]);
			synchronize(view, clear, active).catch(() => {
				if (!active.aborted) report({
					status: "unavailable",
					checked_at: Date.now(),
					native_session_id: view.chatId ?? void 0
				});
			});
		}, signal);
	} finally {
		current?.stop.abort();
	}
}

//#endregion
//#region src/connector-build.ts
const connectorBuild = "0.1.6+19df2cf115a48218";

//#endregion
//#region src/binding-rpc.ts
const channel = "/api";
const endpoint = "lazymind/workflow-binding";
const identifier = /^[A-Za-z0-9_-]{1,256}$/;
function callBinding(ctx, sessionId, signal) {
	return (ctx.get?.("connection"))?.rpc?.call(channel, endpoint, {
		sessionId,
		clientBuild: connectorBuild
	}, signal);
}
function reportClientBuild(ctx, signal) {
	callBinding(ctx, null, signal)?.catch(() => {});
}
function clientBindingLookup(ctx, updateConfig) {
	return async (sessionId, signal) => {
		try {
			const result = object(await callBinding(ctx, sessionId, signal));
			signal.throwIfAborted();
			const value = object(result?.value);
			if (result?.ok !== true || value?.sessionId !== sessionId) return { status: "unavailable" };
			updateConfig(value.config);
			const binding = object(value.binding);
			switch (binding?.status) {
				case "bound":
					if (typeof binding.runId === "string" && identifier.test(binding.runId)) return {
						status: "bound",
						runId: binding.runId
					};
					return { status: "unavailable" };
				case "unbound":
				case "access-denied":
				case "unavailable": return {
					status: binding.status,
					...typeof binding.retryAt === "number" ? { retryAt: binding.retryAt } : {}
				};
				default: return { status: "unavailable" };
			}
		} catch (error) {
			signal.throwIfAborted();
			return error instanceof Error && /HTTP (401|403)$/.test(error.message) ? { status: "access-denied" } : { status: "unavailable" };
		}
	};
}

//#endregion
//#region src/client/index.tsx
const interactiveRegion = { WebkitAppRegion: "no-drag" };
const inject = [
	"uiConversation",
	"slots",
	"connection"
];
function apply(ctx) {
	let config;
	let unregister;
	let viewEpoch = 0;
	const configListeners = /* @__PURE__ */ new Set();
	const subscribeConfig = (listener) => {
		configListeners.add(listener);
		return () => {
			configListeners.delete(listener);
		};
	};
	const configSnapshot = () => config;
	const windows = windowStore();
	ctx.effect(() => () => windows.dispose());
	const definition = {
		kind: "lazymind-workflow",
		target: "chat",
		match(event) {
			const run = config ? eventRun(event, config.serverName, config.webUrl) : null;
			return run ? {
				id: `${run.runId}:${event.seq}`,
				role: "start"
			} : null;
		},
		start(_context, match) {
			const run = config ? eventRun(match.event, config.serverName, config.webUrl) : null;
			if (!run) throw new Error("Workflow presentation requires a valid standard tool result");
			return run;
		},
		update(context) {
			return context.state;
		},
		buildViewNode(context) {
			if (!context.start || !context.state) return null;
			return {
				key: context.key,
				kind: "lazymind-workflow",
				id: context.id,
				target: "chat",
				anchorSeq: context.start.event.seq,
				location: context.start.location,
				visibility: "visible",
				data: context.state
			};
		}
	};
	function Entry({ node, sessionId }) {
		const run = {
			...node.data,
			hostSessionId: sessionId ?? node.data.hostSessionId
		};
		const snapshot = (0, react.useSyncExternalStore)(windows.subscribe, windows.snapshot, windows.snapshot);
		(0, react.useEffect)(() => {
			windows.observe(run, node.anchorSeq);
		}, [
			node.data,
			node.anchorSeq,
			sessionId
		]);
		const first = snapshot.firstCards[runKey(run)];
		if (first === void 0 || first !== node.anchorSeq || snapshot.entries[run.hostSessionId ?? ""]?.run.runId === run.runId) return null;
		return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("section", {
			style: {
				margin: "8px 0",
				border: "1px solid #d9d9d9",
				borderRadius: 8,
				padding: 12
			},
			children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("strong", { children: "LazyMind Workflow" }), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("button", {
				style: { marginLeft: 12 },
				onClick: () => windows.open(run),
				children: "Open workflow"
			})]
		});
	}
	function publishConfig(next) {
		if (JSON.stringify(next) === JSON.stringify(config)) return;
		if (config && config.webUrl !== next?.webUrl) windows.clear();
		config = next;
		unregister?.();
		unregister = config ? ctx.uiConversation.events.register(definition) : void 0;
		for (const listener of configListeners) listener();
	}
	const lookupBinding = clientBindingLookup(ctx, (value) => {
		if (value === null) {
			publishConfig(void 0);
			return;
		}
		try {
			publishConfig(parsePresentationConfig(value));
		} catch {}
	});
	function WorkflowDock({ session }) {
		const connection = (0, react.useSyncExternalStore)(subscribeConfig, configSnapshot, configSnapshot);
		const state = (0, react.useSyncExternalStore)(windows.subscribe, windows.snapshot, windows.snapshot);
		const current = Object.hasOwn(state.entries, session.sessionId) ? state.entries[session.sessionId] : void 0;
		const frame = (0, react.useRef)(null);
		const anchor = (0, react.useRef)(null);
		const [tail, setTail] = (0, react.useState)(null);
		const [expanded, setExpanded] = (0, react.useState)(false);
		const [collapsed, setCollapsed] = (0, react.useState)(false);
		const collapseAnchor = (0, react.useRef)(null);
		const runId = current?.run.runId;
		const link = current && connection ? presentationLink(current.run.runId, connection.webUrl) : null;
		const origin = link ? new URL(link.url).origin : void 0;
		(0, react.useLayoutEffect)(() => {
			const lifetime = new AbortController();
			const chatId = session.sessionId;
			const view = {
				chatId,
				epoch: String(++viewEpoch)
			};
			runPresentation({
				signal: lifetime.signal,
				frameUrl: config?.webUrl ?? "",
				lookupBinding,
				watchView: (report, signal) => {
					report(view);
					return new Promise((resolve) => signal.addEventListener("abort", () => resolve(), { once: true }));
				},
				ui: { async syncPanel(next, target, signal) {
					if (signal.aborted || next.epoch !== view.epoch) return { status: "stale" };
					const run = config && target ? presentationLink(target.runId, config.webUrl) : null;
					windows.binding(chatId, run && {
						...run,
						hostSessionId: chatId
					});
					return { status: run ? "mounted" : "unmounted" };
				} },
				report: (state$1) => {
					if (state$1.status === "access-denied") windows.close(chatId);
				}
			});
			return () => {
				lifetime.abort();
				windows.close(chatId);
			};
		}, [session.sessionId]);
		(0, react.useLayoutEffect)(() => {
			const seat = anchor.current?.closest("[data-composer-seat]");
			const scroller = seat?.parentElement;
			if (!seat || !scroller?.matches("[data-conversation-scroll]")) return;
			const host = document.createElement("div");
			host.dataset.lazymindWorkflowTail = "";
			host.style.cssText = "flex:none;min-width:0;width:100%;position:relative;";
			scroller.insertBefore(host, seat);
			setTail(host);
			return () => {
				host.remove();
			};
		}, [session.sessionId]);
		(0, react.useEffect)(() => {
			setExpanded(false);
			setCollapsed(false);
		}, [runId, session.sessionId]);
		(0, react.useLayoutEffect)(() => {
			const saved = collapseAnchor.current;
			collapseAnchor.current = null;
			const panel$1 = frame.current?.parentElement;
			if (!saved || !panel$1 || expanded || saved.runId !== runId || saved.sessionId !== session.sessionId || saved.scroller !== tail?.parentElement) return;
			saved.scroller.scrollTop += panel$1.getBoundingClientRect().bottom - saved.bottom;
		}, [
			collapsed,
			expanded,
			runId,
			session.sessionId,
			tail
		]);
		(0, react.useEffect)(() => {
			const receive = (event) => {
				if (!origin || event.origin !== origin || event.source !== frame.current?.contentWindow || event.data?.sessionId !== runId) return;
				if (event.data.type === "lazymind.workflow.toggle-expand") setExpanded((value) => {
					if (!value) setCollapsed(false);
					return !value;
				});
				if (event.data.type === "lazymind.workflow.toggle-collapse") {
					const panel$1 = frame.current?.parentElement;
					const scroller = tail?.parentElement;
					if (!expanded && panel$1 && scroller) collapseAnchor.current = {
						bottom: panel$1.getBoundingClientRect().bottom,
						scroller,
						runId,
						sessionId: session.sessionId
					};
					setCollapsed((value) => !value);
				}
			};
			const keydown = (event) => {
				if (event.key === "Escape") setExpanded(false);
			};
			window.addEventListener("message", receive);
			window.addEventListener("keydown", keydown);
			return () => {
				window.removeEventListener("message", receive);
				window.removeEventListener("keydown", keydown);
			};
		}, [
			origin,
			runId,
			expanded,
			tail,
			session.sessionId
		]);
		const syncExpansion = () => {
			if (origin) frame.current?.contentWindow?.postMessage({
				type: "lazymind.workflow.expansion",
				sessionId: runId,
				expanded
			}, origin);
			if (origin) frame.current?.contentWindow?.postMessage({
				type: "lazymind.workflow.collapse",
				sessionId: runId,
				collapsed
			}, origin);
		};
		(0, react.useEffect)(syncExpansion, [
			expanded,
			collapsed,
			origin,
			runId
		]);
		if (!connection) return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)(react_jsx_runtime.Fragment, { children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
			ref: anchor,
			hidden: true
		}), /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("aside", {
			role: "status",
			style: { padding: 8 },
			children: ["LazyMind 尚未连接。请在 LazyMind 中恢复登录。", /* @__PURE__ */ (0, react_jsx_runtime.jsx)("button", {
				onClick: () => window.location.reload(),
				children: "重新加载"
			})]
		})] });
		if (!current || !link) return /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
			ref: anchor,
			hidden: true
		});
		const url = new URL(`${link.url.replace(/\/$/, "")}/embed`);
		url.searchParams.set("hostOrigin", window.location.origin);
		const panel = /* @__PURE__ */ (0, react_jsx_runtime.jsx)("section", {
			"aria-label": "LazyMind Workflow",
			style: {
				...interactiveRegion,
				height: collapsed && !expanded ? 66 : "min(900px, 75dvh)",
				minHeight: 0,
				flexShrink: 0,
				width: "100%",
				maxWidth: "var(--dsh-chat-content-width, 920px)",
				alignSelf: "center",
				boxSizing: "border-box",
				margin: expanded ? 0 : "12px auto",
				display: current.minimized ? "none" : "flex",
				flexDirection: "column",
				...expanded ? {
					position: "fixed",
					inset: "var(--dsh-frame-overlay-top, 16px) 16px 16px",
					width: "auto",
					maxWidth: "none",
					alignSelf: "stretch",
					height: "auto",
					zIndex: 1e3
				} : {},
				background: "#fff",
				borderRadius: 10,
				overflow: "hidden"
			},
			children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)("iframe", {
				ref: frame,
				title: "LazyMind Workflow",
				src: url.href,
				onLoad: syncExpansion,
				style: {
					...interactiveRegion,
					width: "100%",
					height: "100%",
					flex: 1,
					minHeight: 0,
					border: 0
				}
			}, runKey({
				...current.run,
				url: link.url
			}))
		});
		return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)(react_jsx_runtime.Fragment, { children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
			ref: anchor,
			hidden: true
		}), tail ? (0, react_dom.createPortal)(/* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
			style: {
				display: "flow-root",
				height: expanded ? "calc(min(900px, 75dvh) + 24px)" : void 0
			},
			children: panel
		}), tail) : panel] });
	}
	ctx.effect(() => {
		const stop = observePresentationConfig(globalThis, publishConfig);
		const lifetime = new AbortController();
		reportClientBuild(ctx, lifetime.signal);
		return () => {
			lifetime.abort();
			stop();
			unregister?.();
		};
	});
	ctx.slots.inject("conversation.chat.node", () => ctx.slots.register({
		name: "conversation.chat.node",
		key: "lazymind-workflow"
	}, Entry));
	ctx.slots.inject("conversation.input.dock", () => ctx.slots.register({
		name: "conversation.input.dock",
		id: "lazymind-workflow",
		order: -10
	}, WorkflowDock));
}

//#endregion
exports.apply = apply;
exports.connectorBuild = connectorBuild;
exports.inject = inject;
return module.exports; } });