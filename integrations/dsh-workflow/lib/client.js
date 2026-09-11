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
		if (typeof content?.text !== "string") continue;
		const run = runFromToolText(content.text);
		if (run) return run;
	}
	return null;
}

//#endregion
//#region src/client/window-geometry.ts
const RESIZE_EDGES = [
	"n",
	"s",
	"e",
	"w",
	"ne",
	"nw",
	"se",
	"sw"
];
const DEFAULT_WINDOW_SIZE = {
	width: 960,
	height: 720
};
const MIN_WINDOW_SIZE = {
	width: 480,
	height: 360
};
const MARGIN = 20;
function clamp(value, min, max) {
	return Math.min(Math.max(value, min), Math.max(min, max));
}
function sizeBounds(viewport) {
	return {
		minWidth: Math.min(MIN_WINDOW_SIZE.width, viewport.width),
		minHeight: Math.min(MIN_WINDOW_SIZE.height, viewport.height)
	};
}
function clampRect(rect, viewport) {
	const { minWidth, minHeight } = sizeBounds(viewport);
	const width = clamp(rect.width, minWidth, viewport.width);
	const height = clamp(rect.height, minHeight, viewport.height);
	return {
		left: clamp(rect.left, 0, Math.max(0, viewport.width - width)),
		top: clamp(rect.top, 0, Math.max(0, viewport.height - height)),
		width,
		height
	};
}
function defaultWindowRect(viewport) {
	const width = Math.min(DEFAULT_WINDOW_SIZE.width, Math.max(0, viewport.width - MARGIN * 2));
	const height = Math.min(DEFAULT_WINDOW_SIZE.height, Math.max(0, viewport.height - MARGIN * 2));
	return clampRect({
		left: viewport.width - width - MARGIN,
		top: Math.round((viewport.height - height) / 2),
		width,
		height
	}, viewport);
}
function moveRect(start, left, top, viewport) {
	return clampRect({
		...start,
		left,
		top
	}, viewport);
}
function resizeRect(start, edge, delta, viewport) {
	const { minWidth, minHeight } = sizeBounds(viewport);
	let left = start.left;
	let top = start.top;
	let right = start.left + start.width;
	let bottom = start.top + start.height;
	if (edge.includes("e")) right = clamp(start.left + start.width + delta.x, left + minWidth, viewport.width);
	if (edge.includes("s")) bottom = clamp(start.top + start.height + delta.y, top + minHeight, viewport.height);
	if (edge.includes("w")) left = clamp(start.left + delta.x, 0, right - minWidth);
	if (edge.includes("n")) top = clamp(start.top + delta.y, 0, bottom - minHeight);
	return {
		left,
		top,
		width: right - left,
		height: bottom - top
	};
}
function resizeHandleStyle(edge) {
	const base = {
		position: "absolute",
		zIndex: 2,
		touchAction: "none"
	};
	const edgeSize = 6;
	const corner = 14;
	if (edge === "n") return {
		...base,
		top: 0,
		left: corner,
		right: corner,
		height: edgeSize,
		cursor: "ns-resize"
	};
	if (edge === "s") return {
		...base,
		bottom: 0,
		left: corner,
		right: corner,
		height: edgeSize,
		cursor: "ns-resize"
	};
	if (edge === "e") return {
		...base,
		top: corner,
		right: 0,
		bottom: corner,
		width: edgeSize,
		cursor: "ew-resize"
	};
	if (edge === "w") return {
		...base,
		top: corner,
		left: 0,
		bottom: corner,
		width: edgeSize,
		cursor: "ew-resize"
	};
	if (edge === "ne") return {
		...base,
		top: 0,
		right: 0,
		width: corner,
		height: corner,
		cursor: "nesw-resize"
	};
	if (edge === "nw") return {
		...base,
		top: 0,
		left: 0,
		width: corner,
		height: corner,
		cursor: "nwse-resize"
	};
	if (edge === "sw") return {
		...base,
		bottom: 0,
		left: 0,
		width: corner,
		height: corner,
		cursor: "nesw-resize"
	};
	return {
		...base,
		bottom: 0,
		right: 0,
		width: corner,
		height: corner,
		cursor: "nwse-resize"
	};
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
						anchor,
						layout: current?.layout
					}
				} : state.entries
			});
		},
		open(run, anchor) {
			if (!run.hostSessionId) return;
			const prev = state.entries[run.hostSessionId];
			update(run.hostSessionId, {
				run,
				minimized: false,
				anchor,
				layout: prev?.layout
			});
		},
		minimize(id) {
			const entry = state.entries[id];
			if (entry) update(id, {
				...entry,
				minimized: true
			});
		},
		place(id, layout) {
			const entry = state.entries[id];
			if (entry) update(id, {
				...entry,
				layout
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
		const resize = (0, react.useRef)();
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
		const viewport = () => ({
			width: window.innerWidth,
			height: window.innerHeight
		});
		const measured = () => {
			const box = panel.current?.getBoundingClientRect();
			return clampRect(box ? {
				left: box.left,
				top: box.top,
				width: box.width,
				height: box.height
			} : current.layout ?? defaultWindowRect(viewport()), viewport());
		};
		const move = (event) => {
			if (!drag.current) return;
			windows.place(sessionId, moveRect(drag.current.start, event.clientX - drag.current.offsetX, event.clientY - drag.current.offsetY, viewport()));
		};
		const beginDrag = (event) => {
			if (!panel.current || event.target instanceof HTMLButtonElement) return;
			const start = measured();
			drag.current = {
				offsetX: event.clientX - start.left,
				offsetY: event.clientY - start.top,
				start
			};
			windows.place(sessionId, start);
			event.currentTarget.setPointerCapture(event.pointerId);
		};
		const endDrag = () => {
			drag.current = void 0;
		};
		const moveResize = (event) => {
			if (!resize.current) return;
			windows.place(sessionId, resizeRect(resize.current.start, resize.current.edge, {
				x: event.clientX - resize.current.originX,
				y: event.clientY - resize.current.originY
			}, viewport()));
		};
		const beginResize = (edge, event) => {
			event.stopPropagation();
			const start = measured();
			resize.current = {
				edge,
				originX: event.clientX,
				originY: event.clientY,
				start
			};
			windows.place(sessionId, start);
			event.currentTarget.setPointerCapture(event.pointerId);
		};
		const endResize = () => {
			resize.current = void 0;
		};
		const url = new URL(`/workflow-runs/${encodeURIComponent(current.run.runId)}/embed`, new URL(current.run.url).origin).href;
		const layoutStyle = current.layout ? {
			left: current.layout.left,
			top: current.layout.top,
			width: current.layout.width,
			height: current.layout.height
		} : {
			right: 20,
			top: "50%",
			transform: "translateY(-50%)",
			width: "min(960px, calc(100vw - 40px))",
			height: "min(720px, calc(100vh - 40px))"
		};
		return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("section", {
			ref: panel,
			role: "dialog",
			"aria-label": "LazyMind Workflow",
			style: {
				position: "absolute",
				...layoutStyle,
				boxSizing: "border-box",
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
			children: [
				/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("header", {
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
						userSelect: "none",
						flexShrink: 0
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
				}),
				/* @__PURE__ */ (0, react_jsx_runtime.jsx)("iframe", {
					title: "LazyMind Workflow",
					src: url,
					style: {
						width: "100%",
						flex: 1,
						minHeight: 0,
						border: 0
					}
				}),
				RESIZE_EDGES.map((edge) => /* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
					"aria-label": edge === "se" ? "Resize workflow window" : void 0,
					onPointerDown: (event) => beginResize(edge, event),
					onPointerMove: moveResize,
					onPointerUp: endResize,
					onPointerCancel: endResize,
					style: resizeHandleStyle(edge),
					children: edge === "se" ? /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
						"aria-hidden": "true",
						style: {
							position: "absolute",
							right: 4,
							bottom: 4,
							width: 8,
							height: 8,
							borderRight: "2px solid #8c8c8c",
							borderBottom: "2px solid #8c8c8c"
						}
					}) : null
				}, edge))
			]
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