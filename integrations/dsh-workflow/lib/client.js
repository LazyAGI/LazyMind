window.__ModuleLoader__.load({
	id: "@lazymind/dsh-workflow",
	factory: (require) => {
		var module = { exports: {} };
		var exports = module.exports;
		Object.defineProperty(exports, Symbol.toStringTag, { value: "Module" });
		let react = require("react");
		let react_jsx_runtime = require("react/jsx-runtime");
		//#region src/client/index.tsx
		const definition = {
			kind: "lazymind-workflow",
			target: "chat",
			match(event) {
				return event.type === "lazymind-workflow/open" ? {
					id: event.data.runId,
					role: "start"
				} : null;
			},
			start(_context, match) {
				if (match.event.type !== "lazymind-workflow/open") throw new Error("lazymind-workflow start requires lazymind-workflow/open");
				return match.event.data;
			},
			update(context) {
				return context.state;
			},
			buildViewNode(context) {
				if (context.start === void 0) return null;
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
		let state = { minimized: false };
		const listeners = /* @__PURE__ */ new Set();
		const automaticallyOpened = /* @__PURE__ */ new Set();
		function publish(next) {
			state = next;
			for (const listener of listeners) listener();
		}
		function subscribe(listener) {
			listeners.add(listener);
			return () => listeners.delete(listener);
		}
		function openWorkflow(run) {
			publish({
				run,
				minimized: false
			});
		}
		/** Keep a visible launch affordance in the transcript while the panel lives above the app frame. */
		function Panel({ node }) {
			(0, react.useEffect)(() => {
				if (automaticallyOpened.has(node.data.runId)) return;
				automaticallyOpened.add(node.data.runId);
				openWorkflow(node.data);
			}, [node.data]);
			return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("section", {
				style: {
					margin: "8px 0",
					border: "1px solid #d9d9d9",
					borderRadius: 8,
					padding: 12
				},
				children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("strong", { children: "LazyMind Workflow" }), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("button", {
					style: { marginLeft: 12 },
					onClick: () => openWorkflow(node.data),
					children: "Open workflow"
				})]
			});
		}
		/** Normalize a legacy interaction URL to the root-level Workflow Run route. */
		function workflowPage(run) {
			const source = new URL(run.url);
			return new URL(`/workflow-runs/${encodeURIComponent(run.runId)}/embed`, source.origin).href;
		}
		/** Fixed app-frame window: it remains visible while the DSH conversation scrolls. */
		function WorkflowWindow() {
			const current = (0, react.useSyncExternalStore)(subscribe, () => state, () => state);
			const panel = (0, react.useRef)(null);
			const drag = (0, react.useRef)(void 0);
			const [position, setPosition] = (0, react.useState)();
			if (current.run === void 0) return null;
			if (current.minimized) return /* @__PURE__ */ (0, react_jsx_runtime.jsx)("button", {
				style: {
					position: "absolute",
					right: 24,
					bottom: 24,
					zIndex: 1
				},
				onClick: () => publish({
					...current,
					minimized: false
				}),
				children: "Open LazyMind Workflow"
			});
			const move = (event) => {
				if (drag.current === void 0 || panel.current === null) return;
				const width = panel.current.offsetWidth;
				const height = panel.current.offsetHeight;
				setPosition({
					left: Math.max(0, Math.min(window.innerWidth - width, event.clientX - drag.current.offsetX)),
					top: Math.max(0, Math.min(window.innerHeight - height, event.clientY - drag.current.offsetY))
				});
			};
			const beginDrag = (event) => {
				if (panel.current === null || event.target instanceof HTMLButtonElement) return;
				const rect = panel.current.getBoundingClientRect();
				drag.current = {
					offsetX: event.clientX - rect.left,
					offsetY: event.clientY - rect.top
				};
				setPosition({
					left: rect.left,
					top: rect.top
				});
				event.currentTarget.setPointerCapture(event.pointerId);
			};
			const endDrag = () => {
				drag.current = void 0;
			};
			return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("section", {
				ref: panel,
				role: "dialog",
				"aria-label": "LazyMind Workflow",
				style: {
					position: "absolute",
					...position === void 0 ? {
						right: 20,
						top: "50%",
						transform: "translateY(-50%)"
					} : position,
					width: "min(760px, calc(100vw - 40px))",
					height: "min(460px, calc(100vh - 40px))",
					background: "#fff",
					color: "#111",
					border: "1px solid #d9d9d9",
					borderRadius: 10,
					boxShadow: "0 12px 48px rgba(0, 0, 0, .24)",
					overflow: "hidden",
					display: "flex",
					flexDirection: "column",
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
						onClick: () => publish({
							...current,
							minimized: true
						}),
						children: "Minimize"
					}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("button", {
						style: { marginLeft: 8 },
						onPointerDown: (event) => event.stopPropagation(),
						onClick: () => publish({ minimized: false }),
						children: "Close"
					})] })]
				}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("iframe", {
					title: "LazyMind Workflow",
					src: workflowPage(current.run),
					style: {
						width: "100%",
						flex: 1,
						border: 0
					}
				})]
			});
		}
		/** Required browser services for the durable Conversation Node and its renderer. */
		const inject = ["uiConversation", "slots"];
		/** Register the external run event and its WorkflowPanel iframe renderer. */
		function apply(ctx) {
			ctx.uiConversation.events.register(definition);
			ctx.slots.inject("conversation.chat.node", () => ctx.slots.register({
				name: "conversation.chat.node",
				key: "lazymind-workflow"
			}, Panel));
			ctx.slots.inject("shell.overlay", () => ctx.slots.register({
				name: "shell.overlay",
				id: "lazymind-workflow-window"
			}, WorkflowWindow));
		}
		//#endregion
		exports.apply = apply;
		exports.inject = inject;
		return module.exports;
	}
});
