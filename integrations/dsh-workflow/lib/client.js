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
			if (!run.hostSessionId) return;
			update(run.hostSessionId, {
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
				onClick: () => windows.open(run, node.anchorSeq),
				children: "Open workflow"
			})]
		});
	}
	function WorkflowDock({ session }) {
		const current = (0, react.useSyncExternalStore)(windows.subscribe, windows.snapshot, windows.snapshot).entries[session.sessionId];
		const frame = (0, react.useRef)(null);
		const [expanded, setExpanded] = (0, react.useState)(false);
		const [collapsed, setCollapsed] = (0, react.useState)(false);
		const runId = current?.run.runId;
		const origin = current ? new URL(current.run.url).origin : void 0;
		(0, react.useEffect)(() => {
			setExpanded(false);
			setCollapsed(false);
		}, [runId, session.sessionId]);
		(0, react.useEffect)(() => {
			const receive = (event) => {
				if (!origin || event.origin !== origin || event.source !== frame.current?.contentWindow || event.data?.sessionId !== runId) return;
				if (event.data.type === "lazymind.workflow.toggle-expand") setExpanded((value) => {
					if (!value) setCollapsed(false);
					return !value;
				});
				if (event.data.type === "lazymind.workflow.toggle-collapse") setCollapsed((value) => !value);
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
		}, [origin, runId]);
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
		if (!current) return null;
		const url = new URL(`/workflow-runs/${encodeURIComponent(current.run.runId)}/embed`, origin);
		url.searchParams.set("hostOrigin", window.location.origin);
		return /* @__PURE__ */ (0, react_jsx_runtime.jsx)("section", {
			"aria-label": "LazyMind Workflow",
			style: {
				height: collapsed && !expanded ? 66 : "min(480px, 55dvh)",
				minHeight: 0,
				flexShrink: 0,
				width: "100%",
				maxWidth: "var(--dsh-chat-content-width, 920px)",
				alignSelf: "center",
				boxSizing: "border-box",
				display: current.minimized ? "none" : "flex",
				flexDirection: "column",
				...expanded ? {
					position: "fixed",
					inset: 16,
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
					width: "100%",
					height: "100%",
					flex: 1,
					minHeight: 0,
					border: 0
				}
			}, runKey(current.run))
		});
	}
	ctx.uiConversation.events.register(definition);
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
exports.inject = inject;
return module.exports; } });