window.__ModuleLoader__.load({ id: "@lazymind/dsh-workflow", factory: (require) => {
var module = { exports: {} }; var exports = module.exports;
let react = require("react");
let react_jsx_runtime = require("react/jsx-runtime");

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
//#region src/client/window-store.ts
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
	const update = (id, entry) => publish({
		...state,
		entries: {
			...state.entries,
			[id]: entry
		}
	});
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
			const current = state.entries[run.hostSessionId];
			const shouldOpen = !current || run.operation === "start" && run.runId !== current.run.runId && anchor > current.anchor;
			if (first === void 0 || anchor < first || shouldOpen) publish({
				firstCards: {
					...state.firstCards,
					[key]: first === void 0 ? anchor : Math.min(first, anchor)
				},
				entries: shouldOpen ? {
					...state.entries,
					[run.hostSessionId]: {
						run,
						minimized: false,
						anchor
					}
				} : state.entries
			});
		},
		open(run, anchor) {
			if (run.hostSessionId) update(run.hostSessionId, {
				run,
				minimized: false,
				anchor
			});
		},
		minimize(id) {
			const entry = state.entries[id];
			if (entry) update(id, {
				...entry,
				minimized: true
			});
		},
		position(id, position) {
			const entry = state.entries[id];
			if (entry) update(id, {
				...entry,
				position
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
//#region src/client/index.tsx
const inject = ["uiConversation", "slots"];
function apply(ctx, config = {}) {
	const windows = windowStore();
	const serverName = config.serverName ?? "lazymind";
	ctx.effect(() => () => windows.dispose());
	const definition = {
		kind: "lazymind-workflow",
		target: "chat",
		match(event) {
			const run = eventRun(event, serverName);
			return run ? {
				id: `${run.runId}:${event.seq}`,
				role: "start"
			} : null;
		},
		start(_context, match) {
			const run = eventRun(match.event, serverName);
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
		const run = node.data.hostSessionId ? node.data : {
			...node.data,
			hostSessionId: sessionId
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
		if (first !== void 0 && first !== node.anchorSeq) return null;
		return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("section", {
			style: {
				margin: "8px 0",
				border: "1px solid #d9d9d9",
				borderRadius: 8,
				padding: 12
			},
			children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("strong", { children: "LazyMind Workflow" }), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("button", {
				style: { marginLeft: 12 },
				onClick: () => windows.open(run, node.anchorSeq),
				children: "Open workflow"
			})]
		});
	}
	function WorkflowWindow({ useSessions }) {
		const sessionId = useSessions((state$1) => state$1.current);
		const state = (0, react.useSyncExternalStore)(windows.subscribe, windows.snapshot, windows.snapshot);
		const current = sessionId ? state.entries[sessionId] : void 0;
		const panel = (0, react.useRef)(null);
		const drag = (0, react.useRef)();
		if (!current || !sessionId) return null;
		if (current.minimized) return /* @__PURE__ */ (0, react_jsx_runtime.jsx)("button", {
			style: {
				position: "absolute",
				right: 24,
				bottom: 24,
				pointerEvents: "auto",
				zIndex: 1
			},
			onClick: () => windows.open(current.run, current.anchor),
			children: "Open LazyMind Workflow"
		});
		const move = (event) => {
			if (!drag.current || !panel.current) return;
			windows.position(sessionId, {
				left: Math.max(0, Math.min(window.innerWidth - panel.current.offsetWidth, event.clientX - drag.current.offsetX)),
				top: Math.max(0, Math.min(window.innerHeight - panel.current.offsetHeight, event.clientY - drag.current.offsetY))
			});
		};
		const beginDrag = (event) => {
			if (!panel.current || event.target instanceof HTMLButtonElement) return;
			const rect = panel.current.getBoundingClientRect();
			drag.current = {
				offsetX: event.clientX - rect.left,
				offsetY: event.clientY - rect.top
			};
			windows.position(sessionId, {
				left: rect.left,
				top: rect.top
			});
			event.currentTarget.setPointerCapture(event.pointerId);
		};
		const endDrag = () => {
			drag.current = void 0;
		};
		const url = new URL(`/workflow-runs/${encodeURIComponent(current.run.runId)}/embed`, new URL(current.run.url).origin).href;
		return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("section", {
			ref: panel,
			role: "dialog",
			"aria-label": "LazyMind Workflow",
			style: {
				position: "absolute",
				...current.position ?? {
					right: 20,
					top: "50%",
					transform: "translateY(-50%)"
				},
				width: "min(760px, calc(100vw - 40px))",
				height: "min(560px, calc(100vh - 40px))",
				background: "#fff",
				color: "#111",
				border: "1px solid #d9d9d9",
				borderRadius: 10,
				boxShadow: "0 12px 48px rgba(0, 0, 0, .24)",
				overflow: "hidden",
				display: "flex",
				flexDirection: "column",
				pointerEvents: "auto",
				zIndex: 1
			},
			children: [/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("header", {
				onPointerDown: beginDrag,
				onPointerMove: move,
				onPointerUp: endDrag,
				onPointerCancel: endDrag,
				style: {
					display: "flex",
					alignItems: "center",
					justifyContent: "space-between",
					gap: 12,
					padding: 12,
					borderBottom: "1px solid #d9d9d9",
					cursor: "grab",
					touchAction: "none",
					userSelect: "none"
				},
				children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("strong", { children: "LazyMind Workflow" }), /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("span", { children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("button", {
					onPointerDown: (event) => event.stopPropagation(),
					onClick: () => windows.minimize(sessionId),
					children: "Minimize"
				}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("button", {
					style: { marginLeft: 8 },
					onPointerDown: (event) => event.stopPropagation(),
					onClick: () => windows.minimize(sessionId),
					children: "Close"
				})] })]
			}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("iframe", {
				title: "LazyMind Workflow",
				src: url,
				style: {
					width: "100%",
					flex: 1,
					border: 0
				}
			})]
		});
	}
	ctx.uiConversation.events.register(definition);
	ctx.slots.inject("conversation.chat.node", () => ctx.slots.register({
		name: "conversation.chat.node",
		key: "lazymind-workflow"
	}, Entry));
	ctx.slots.inject("shell.overlay", () => ctx.slots.register({
		name: "shell.overlay",
		id: "lazymind-workflow-window"
	}, WorkflowWindow));
}

//#endregion
exports.apply = apply;
exports.inject = inject;
return module.exports; } });