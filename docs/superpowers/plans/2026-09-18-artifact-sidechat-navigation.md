# Artifact Side-chat Navigation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make side chat exclusively occupy the ordinary right sidebar and remove the conversation-menu branch entry without changing workflow or message-level branch behavior.

**Architecture:** `ChatLayout` owns ordinary right-sidebar state. Opening side chat clears the artifact request before it is displayed; closing it leaves no previous panel selected. The overflow menu stops exposing the latest-reply branch action, while existing message-level `onFork` remains intact.

**Tech Stack:** React 18, TypeScript, Ant Design, Vitest, Testing Library.

## Global Constraints

- Do not alter server-side fork capability, `useForkConversation`, or message-level branching.
- Do not alter side-chat API lifecycle, retention, discard, or streaming behavior.
- Keep workflow expanded rail and source-reference panel priority unchanged.
- Cover changed navigation behavior with `ChatLayout` integration tests.

---

### Task 1: Prove and change ordinary sidebar selection behavior

**Files:**

- Modify: `frontend/src/modules/chat/pages/chatLayout/index.test.tsx`
- Modify: `frontend/src/modules/chat/pages/chatLayout/index.tsx`

**Interfaces:**

- Consumes: `handleOpenSideChat(source?: SideChatSource)`, `isArtifactPanelRequested`, and `SideChatPanel`'s close callback.
- Produces: side chat replaces files in the ordinary `right-box`; its close action leaves no ordinary sidebar panel visible.

- [ ] **Step 1: Write the failing test**

Add an integration test that dispatches `lazymind:chat-open-artifact-panel` for `source`, opens side chat through the overflow menu, and asserts the artifact panel is absent while the side-chat panel is visible. Trigger the side-chat close control and assert that neither panel nor the ordinary `.right-box` is visible.

```tsx
expect(screen.queryByTestId("artifact-panel")).not.toBeInTheDocument();
expect(screen.getByTestId("side-chat-panel")).toHaveAttribute("data-visible", "true");
fireEvent.click(screen.getByRole("button", { name: "chat.sideChat.close" }));
expect(screen.queryByTestId("side-chat-panel")).not.toBeInTheDocument();
expect(document.querySelector(".right-box:not([hidden])")).toBeNull();
```

- [ ] **Step 2: Run test to verify it fails**

Run: `pnpm test src/modules/chat/pages/chatLayout/index.test.tsx`

Expected: FAIL because the artifact panel retains selection and wins the ordinary sidebar priority after side chat opens.

- [ ] **Step 3: Write minimal implementation**

In `handleOpenSideChat`, clear the selected artifact panel and reset ordinary sidebar width before adding the side-chat source. Do not add a restore state. The existing `onClose` deletion then makes `showOrdinaryRightBox` false when no task panel is active.

```tsx
setPanelWidth(0);
setIsArtifactPanelRequested(false);
setIsTaskPanelCollapsed(true);
setWorkflowPanelExpanded(false);
setSideChats((current) => ({ ...current, [sessionIdRef.current]: source }));
```

- [ ] **Step 4: Run test to verify it passes**

Run: `pnpm test src/modules/chat/pages/chatLayout/index.test.tsx`

Expected: PASS, including artifact/task/source-reference regressions.

- [ ] **Step 5: Commit**

Run:

```bash
git add frontend/src/modules/chat/pages/chatLayout/index.tsx frontend/src/modules/chat/pages/chatLayout/index.test.tsx
git commit -m "fix(chat): make side chat replace files panel"
```

### Task 2: Remove the overflow-menu branch entry

**Files:**

- Modify: `frontend/src/modules/chat/pages/chatLayout/index.test.tsx`
- Modify: `frontend/src/modules/chat/pages/chatLayout/index.tsx`

**Interfaces:**

- Consumes: overflow menu items for `conversation-files` and `open-side-chat`.
- Produces: no `fork-from-latest` menu item or handler; message-level `onFork` remains unchanged.

- [ ] **Step 1: Write the failing test**

Update the menu integration test to assert the files and side-chat actions remain available and the branch action is not rendered, even when fork capability is supported.

```tsx
expect(screen.getByTestId("conversation-menu-conversation-files")).toBeInTheDocument();
expect(screen.getByTestId("conversation-menu-open-side-chat")).toBeInTheDocument();
expect(screen.queryByTestId("conversation-menu-fork-from-latest")).not.toBeInTheDocument();
```

- [ ] **Step 2: Run test to verify it fails**

Run: `pnpm test src/modules/chat/pages/chatLayout/index.test.tsx`

Expected: FAIL because the supported conversation currently renders `fork-from-latest`.

- [ ] **Step 3: Write minimal implementation**

Remove `handleForkFromLatest` and delete the conditional `fork-from-latest` menu item and handler branch. Keep `fork`, `forkSupported`, and `onFork={forkSupported ? fork.begin : undefined}` because those retain message-level branching.

```tsx
items: [
  { key: "conversation-files", label: t("chat.artifactPanelOpenMenu") },
  ...(canOpenSideChat
    ? [{ key: "open-side-chat", label: t("chat.sideChat.openPanel") }]
    : []),
]
```

- [ ] **Step 4: Run test to verify it passes**

Run: `pnpm test src/modules/chat/pages/chatLayout/index.test.tsx`

Expected: PASS, with `ChatContainerComponent` retaining its supported-conversation `onFork` prop.

- [ ] **Step 5: Commit**

Run:

```bash
git add frontend/src/modules/chat/pages/chatLayout/index.tsx frontend/src/modules/chat/pages/chatLayout/index.test.tsx
git commit -m "fix(chat): remove overflow branch action"
```

### Task 3: Verify the focused frontend surface

**Files:**

- Verify: `frontend/src/modules/chat/pages/chatLayout/index.test.tsx`
- Verify: `frontend/src/modules/chat/pages/chatLayout/index.tsx`

**Interfaces:**

- Consumes: completed ordinary-sidebar and menu changes.
- Produces: evidence that targeted tests, TypeScript, linting, and build accept the changed surface.

- [ ] **Step 1: Run focused tests**

Run: `pnpm test src/modules/chat/pages/chatLayout/index.test.tsx`

Expected: PASS with no failed tests.

- [ ] **Step 2: Run static checks**

Run: `pnpm lint && pnpm typecheck:all`

Expected: both commands exit 0.

- [ ] **Step 3: Run production build**

Run: `pnpm build`

Expected: Vite build exits 0.

- [ ] **Step 4: Inspect scoped diff**

Run: `git diff --check -- frontend/src/modules/chat/pages/chatLayout/index.tsx frontend/src/modules/chat/pages/chatLayout/index.test.tsx`

Expected: no whitespace errors; only planned menu and sidebar state changes appear.
