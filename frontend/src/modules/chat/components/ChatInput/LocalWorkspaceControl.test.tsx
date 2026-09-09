import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import {
  afterAll,
  afterEach,
  beforeAll,
  beforeEach,
  describe,
  expect,
  it,
  vi,
} from "vitest";
import { Modal, message } from "antd";
import { axiosInstance } from "@/components/request";

import type { LocalWorkspaceView, WorkspaceApproval } from "@/modules/chat/utils/localWorkspace";
import LocalWorkspaceControl from "./LocalWorkspaceControl";

const mocks = vi.hoisted(() => ({
  authorizeWorkspace: vi.fn(),
  getConversationWorkspace: vi.fn(),
  getRuntimeMode: vi.fn(),
  listWorkspaces: vi.fn(),
  prepareWorkspaceReauthorization: vi.fn(),
  revokeWorkspace: vi.fn(),
  selectWorkspaceCandidate: vi.fn(),
  updateWorkspacePermission: vi.fn(),
}));

vi.mock("@/runtime/mode", () => ({
  getRuntimeMode: mocks.getRuntimeMode,
}));

vi.mock("@/components/request", () => ({ BASE_URL: "", axiosInstance: { get: vi.fn(), post: vi.fn(), put: vi.fn() } }));

vi.mock("@/modules/chat/utils/localWorkspace", async () => ({
  ...await vi.importActual<typeof import("@/modules/chat/utils/localWorkspace")>("@/modules/chat/utils/localWorkspace"),
  authorizeWorkspace: mocks.authorizeWorkspace,
  getConversationWorkspace: mocks.getConversationWorkspace,
  listWorkspaces: mocks.listWorkspaces,
  prepareWorkspaceReauthorization: mocks.prepareWorkspaceReauthorization,
  revokeWorkspace: mocks.revokeWorkspace,
  selectWorkspaceCandidate: mocks.selectWorkspaceCandidate,
  updateWorkspacePermission: mocks.updateWorkspacePermission,
}));

vi.mock("react-i18next", () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}));

vi.mock("antd", async () => {
  const actual = await vi.importActual<typeof import("antd")>("antd");
  return {
    ...actual,
    message: { error: vi.fn(), success: vi.fn() },
  };
});

const alpha: LocalWorkspaceView = {
  workspace_id: "grant-alpha",
  display_name: "Alpha",
  path: "/workspace/alpha",
  status: "active",
  version: 2,
  source: "local",
  affected_task_count: 1,
  permission_mode: "ask_as_needed",
  permission_version: 3,
};

const beta: LocalWorkspaceView = {
  ...alpha,
  workspace_id: "grant-beta",
  display_name: "Beta",
  path: "/workspace/beta",
};

function deferred<T>() {
  let resolve!: (value: T | PromiseLike<T>) => void;
  const promise = new Promise<T>((next) => {
    resolve = next;
  });
  return { promise, resolve };
}

const getComputedStyle = window.getComputedStyle.bind(window);

async function openCandidateDialog(onChange: ReturnType<typeof vi.fn>) {
  mocks.selectWorkspaceCandidate.mockResolvedValue({
    canceled: false,
    selection_token: "selection-token",
    display_name: "Alpha",
    path: alpha.path,
  });
  render(<LocalWorkspaceControl onChange={onChange} />);
  fireEvent.click(screen.getByRole("button", { name: /chat\.workspace\.select/ }));
  return screen.findByRole("dialog");
}

async function findConfirmDialog(title: string) {
  const titles = await screen.findAllByText(title);
  const confirmTitle = titles.find((item) =>
    item.classList.contains("ant-modal-confirm-title"),
  );
  const dialog = confirmTitle?.closest<HTMLElement>("[role=dialog]");
  if (!dialog) throw new Error(`${title} dialog missing`);
  return dialog;
}

describe("LocalWorkspaceControl task binding and request lifetime", () => {
  beforeAll(() => {
    vi.spyOn(window, "getComputedStyle").mockImplementation((element) =>
      getComputedStyle(element),
    );
  });

  beforeEach(() => {
    vi.clearAllMocks();
    Object.defineProperty(document, "visibilityState", { configurable: true, value: "visible" });
    vi.mocked(axiosInstance.get).mockReset().mockResolvedValue({ data: { data: { items: [] } } });
    vi.mocked(axiosInstance.post).mockReset().mockResolvedValue({ data: { data: { status: "allowed" } } });
    mocks.getRuntimeMode.mockReturnValue("local");
    mocks.listWorkspaces.mockResolvedValue([]);
    mocks.getConversationWorkspace.mockResolvedValue(undefined);
    mocks.selectWorkspaceCandidate.mockResolvedValue({ canceled: true });
    mocks.authorizeWorkspace.mockResolvedValue(alpha);
    mocks.updateWorkspacePermission.mockResolvedValue({
      permission_mode: "always_ask",
      permission_version: 4,
      effective_at: "next_request",
    });
  });

  afterEach(() => {
    Modal.destroyAll();
    cleanup();
    vi.useRealTimers();
  });
  afterAll(() => vi.restoreAllMocks());

  it("locks folder selection for an existing bound task", async () => {
    mocks.getConversationWorkspace.mockResolvedValue(alpha);
    const onChange = vi.fn();

    render(<LocalWorkspaceControl conversationId="conv-alpha" onChange={onChange} />);

    expect(await screen.findByRole("button", { name: /Alpha/ })).toBeDisabled();
    expect(mocks.selectWorkspaceCandidate).not.toHaveBeenCalled();
  });

  it("does not allow a workspace to be added to an existing unbound task", async () => {
    const onChange = vi.fn();
    render(<LocalWorkspaceControl conversationId="conv-unbound" onChange={onChange} />);

    await waitFor(() => {
      expect(mocks.getConversationWorkspace).toHaveBeenCalledWith("conv-unbound");
    });
    expect(screen.getByRole("button", { name: /chat\.workspace\.select/ })).toBeDisabled();
  });

  it("clears the visible and parent workspace immediately when the conversation changes", async () => {
    const nextConversation = deferred<LocalWorkspaceView | undefined>();
    mocks.getConversationWorkspace.mockImplementation((conversationId: string) =>
      conversationId === "conv-alpha" ? Promise.resolve(alpha) : nextConversation.promise,
    );
    const onChange = vi.fn();
    const view = render(
      <LocalWorkspaceControl conversationId="conv-alpha" onChange={onChange} />,
    );
    expect(await screen.findByText(alpha.path)).toBeInTheDocument();
    onChange.mockClear();

    view.rerender(
      <LocalWorkspaceControl conversationId="conv-unbound" onChange={onChange} />,
    );

    expect(screen.queryAllByText(alpha.path)).toHaveLength(0);
    expect(onChange).toHaveBeenCalledWith(undefined, "ask_as_needed");
  });

  it("ignores a binding lookup that finishes after a newer conversation", async () => {
    const oldLookup = deferred<LocalWorkspaceView | undefined>();
    mocks.getConversationWorkspace.mockImplementation((conversationId: string) =>
      conversationId === "conv-alpha" ? oldLookup.promise : Promise.resolve(beta),
    );
    const view = render(
      <LocalWorkspaceControl conversationId="conv-alpha" onChange={vi.fn()} />,
    );

    view.rerender(
      <LocalWorkspaceControl conversationId="conv-beta" onChange={vi.fn()} />,
    );
    expect(await screen.findByText(beta.path)).toBeInTheDocument();
    await act(async () => oldLookup.resolve(alpha));

    expect(screen.getByText(beta.path)).toBeInTheDocument();
    expect(screen.queryByText(alpha.path)).not.toBeInTheDocument();
  });

  it("ignores a folder picker result from the previous conversation", async () => {
    const picker = deferred<{
      canceled: boolean;
      selection_token?: string;
      display_name?: string;
      path?: string;
    }>();
    mocks.selectWorkspaceCandidate.mockReturnValue(picker.promise);
    const onChange = vi.fn();
    const view = render(<LocalWorkspaceControl onChange={onChange} />);
    fireEvent.click(screen.getByRole("button", { name: /chat\.workspace\.select/ }));
    await waitFor(() => expect(mocks.selectWorkspaceCandidate).toHaveBeenCalled());

    view.rerender(
      <LocalWorkspaceControl conversationId="conv-unbound" onChange={onChange} />,
    );
    await act(async () =>
      picker.resolve({
        canceled: false,
        selection_token: "old-token",
        display_name: "Old selection",
        path: "/workspace/old",
      }),
    );

    expect(screen.queryByText("/workspace/old")).not.toBeInTheDocument();
    expect(onChange).not.toHaveBeenCalled();
  });

  it("does not apply a completed authorization to a newer conversation", async () => {
    const authorization = deferred<LocalWorkspaceView>();
    mocks.selectWorkspaceCandidate.mockResolvedValue({
      canceled: false,
      selection_token: "selection-token",
      display_name: "Alpha",
      path: alpha.path,
    });
    mocks.authorizeWorkspace.mockReturnValue(authorization.promise);
    const onChange = vi.fn();
    const view = render(<LocalWorkspaceControl onChange={onChange} />);
    fireEvent.click(screen.getByRole("button", { name: /chat\.workspace\.select/ }));
    const dialog = await screen.findByRole("dialog");
    fireEvent.click(
      within(dialog).getByRole("button", { name: "chat.workspace.authorize" }),
    );
    await waitFor(() => expect(mocks.authorizeWorkspace).toHaveBeenCalled());

    view.rerender(
      <LocalWorkspaceControl conversationId="conv-unbound" onChange={onChange} />,
    );
    await act(async () => authorization.resolve(alpha));

    expect(screen.queryAllByText(alpha.path)).toHaveLength(0);
    expect(onChange).not.toHaveBeenCalled();
  });

  it("keeps selection unchanged when the native picker is canceled", async () => {
    const onChange = vi.fn();
    render(<LocalWorkspaceControl onChange={onChange} />);

    fireEvent.click(screen.getByRole("button", { name: /chat\.workspace\.select/ }));
    await waitFor(() => expect(mocks.selectWorkspaceCandidate).toHaveBeenCalled());

    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(mocks.authorizeWorkspace).not.toHaveBeenCalled();
    expect(onChange).not.toHaveBeenCalled();
  });

  it("keeps a same-draft workspace list when the native picker is canceled", async () => {
    const list = deferred<LocalWorkspaceView[]>();
    mocks.listWorkspaces.mockReturnValue(list.promise);
    render(<LocalWorkspaceControl onChange={vi.fn()} />);

    fireEvent.click(screen.getByRole("button", { name: /chat\.workspace\.select/ }));
    await waitFor(() => expect(mocks.selectWorkspaceCandidate).toHaveBeenCalled());
    await act(async () => list.resolve([alpha]));

    expect(await screen.findByRole("combobox")).toBeInTheDocument();
  });

  it.each([
    [
      "Cancel button",
      (dialog: HTMLElement) =>
        fireEvent.click(within(dialog).getByRole("button", { name: "Cancel" })),
    ],
    [
      "close button",
      (dialog: HTMLElement) =>
        fireEvent.click(within(dialog).getByRole("button", { name: "Close" })),
    ],
    [
      "Escape key",
      (dialog: HTMLElement) => {
        const wrap = dialog.closest(".ant-modal-wrap");
        if (!wrap) throw new Error("modal keyboard container missing");
        fireEvent.keyDown(wrap, {
          key: "Escape",
          code: "Escape",
          keyCode: 27,
          which: 27,
        });
      },
    ],
  ])("closes authorization with the %s without side effects", async (_label, close) => {
    const onChange = vi.fn();
    const dialog = await openCandidateDialog(onChange);

    close(dialog);

    await waitFor(() => expect(dialog).toHaveClass("ant-zoom-leave"));
    expect(mocks.authorizeWorkspace).not.toHaveBeenCalled();
    expect(onChange).not.toHaveBeenCalled();
  });

  it("submits only the selection token when authorization is confirmed", async () => {
    mocks.selectWorkspaceCandidate.mockResolvedValue({
      canceled: false,
      selection_token: "selection-token",
      display_name: "Alpha",
      path: alpha.path,
    });
    const onChange = vi.fn();
    render(<LocalWorkspaceControl onChange={onChange} />);

    fireEvent.click(screen.getByRole("button", { name: /chat\.workspace\.select/ }));
    const dialog = await screen.findByRole("dialog");
    fireEvent.click(
      within(dialog).getByRole("button", { name: "chat.workspace.authorize" }),
    );

    await waitFor(() => {
      expect(mocks.authorizeWorkspace).toHaveBeenCalledWith(
        "local",
        "selection-token",
      );
    });
    expect(onChange).toHaveBeenCalledWith(alpha.workspace_id, "ask_as_needed");
  });

  it("disables the recent-workspace selector with the draft", async () => {
    mocks.listWorkspaces.mockResolvedValue([alpha]);
    render(<LocalWorkspaceControl disabled onChange={vi.fn()} />);

    expect(await screen.findByRole("combobox")).toBeDisabled();
  });

  it("keeps next-request permission editing available while task input is disabled", async () => {
    mocks.getConversationWorkspace.mockResolvedValue(alpha);
    render(
      <LocalWorkspaceControl
        conversationId="conv-alpha"
        disabled
        onChange={vi.fn()}
      />,
    );
    expect(await screen.findByText(alpha.path)).toBeInTheDocument();
    const permission = screen.getByRole("combobox");

    expect(permission).toBeEnabled();
    fireEvent.mouseDown(permission);
    fireEvent.click(await screen.findByText("chat.workspace.everyAsk"));
    await waitFor(() => {
      expect(mocks.updateWorkspacePermission).toHaveBeenCalledWith(
        "conv-alpha",
        "always_ask",
        alpha.permission_version,
      );
    });
  });

  it("ignores a permission update that finishes after the conversation changes", async () => {
    const update = deferred<{
      permission_mode: "always_ask";
      permission_version: number;
      effective_at: "next_request";
    }>();
    mocks.updateWorkspacePermission.mockReturnValue(update.promise);
    mocks.getConversationWorkspace.mockImplementation((conversationId: string) =>
      Promise.resolve(conversationId === "conv-alpha" ? alpha : beta),
    );
    const onChange = vi.fn();
    const view = render(
      <LocalWorkspaceControl conversationId="conv-alpha" onChange={onChange} />,
    );
    expect(await screen.findByText(alpha.path)).toBeInTheDocument();
    fireEvent.mouseDown(screen.getByRole("combobox"));
    fireEvent.click(await screen.findByText("chat.workspace.everyAsk"));
    await waitFor(() => expect(mocks.updateWorkspacePermission).toHaveBeenCalled());

    view.rerender(
      <LocalWorkspaceControl conversationId="conv-beta" onChange={onChange} />,
    );
    expect(await screen.findByText(beta.path)).toBeInTheDocument();
    onChange.mockClear();
    await act(async () =>
      update.resolve({
        permission_mode: "always_ask",
        permission_version: 4,
        effective_at: "next_request",
      }),
    );

    expect(screen.getByText(beta.path)).toBeInTheDocument();
    expect(onChange).not.toHaveBeenCalled();
  });

  it("does not confirm allow-all for a conversation that is no longer current", async () => {
    mocks.getConversationWorkspace.mockImplementation((conversationId: string) =>
      Promise.resolve(conversationId === "conv-alpha" ? alpha : beta),
    );
    const view = render(
      <LocalWorkspaceControl conversationId="conv-alpha" onChange={vi.fn()} />,
    );
    expect(await screen.findByText(alpha.path)).toBeInTheDocument();
    fireEvent.mouseDown(screen.getByRole("combobox"));
    fireEvent.click(await screen.findByText("chat.workspace.allowAll"));
    const dialog = await findConfirmDialog("chat.workspace.allowAllTitle");

    view.rerender(
      <LocalWorkspaceControl conversationId="conv-beta" onChange={vi.fn()} />,
    );
    expect(await screen.findByText(beta.path)).toBeInTheDocument();
    fireEvent.click(within(dialog).getByRole("button", { name: "OK" }));

    expect(mocks.updateWorkspacePermission).not.toHaveBeenCalled();
  });

  it("ignores a revoke result that finishes after the conversation changes", async () => {
    const revoke = deferred<{ version: number; stop_failed_count: number }>();
    mocks.revokeWorkspace.mockReturnValue(revoke.promise);
    mocks.getConversationWorkspace.mockImplementation((conversationId: string) =>
      Promise.resolve(conversationId === "conv-alpha" ? alpha : beta),
    );
    const onChange = vi.fn();
    const view = render(
      <LocalWorkspaceControl conversationId="conv-alpha" onChange={onChange} />,
    );
    expect(await screen.findByText(alpha.path)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "chat.workspace.revoke" }));
    const dialog = await findConfirmDialog("chat.workspace.revokeTitle");
    fireEvent.click(within(dialog).getByRole("button", { name: "OK" }));
    await waitFor(() => expect(mocks.revokeWorkspace).toHaveBeenCalled());

    view.rerender(
      <LocalWorkspaceControl conversationId="conv-beta" onChange={onChange} />,
    );
    expect(await screen.findByText(beta.path)).toBeInTheDocument();
    onChange.mockClear();
    await act(async () => revoke.resolve({ version: 3, stop_failed_count: 0 }));

    expect(screen.getByText(beta.path)).toBeInTheDocument();
    expect(onChange).not.toHaveBeenCalled();
  });

  it("searches active and inactive grants from the access manager", async () => {
    mocks.listWorkspaces.mockResolvedValueOnce([]).mockResolvedValueOnce([alpha, { ...beta, status: "revoked" }]).mockResolvedValue([]);
    render(<LocalWorkspaceControl onChange={vi.fn()} />);
    fireEvent.click(screen.getByRole("button", { name: /chat\.workspace\.manage/ }));
    expect(await screen.findByText(beta.path)).toBeInTheDocument();

    const search = screen.getByPlaceholderText("chat.workspace.search");
    fireEvent.change(search, { target: { value: "beta" } });
    fireEvent.keyDown(search, { key: "Enter", code: "Enter" });
    await waitFor(() => expect(mocks.listWorkspaces).toHaveBeenLastCalledWith({ query: "beta", includeInactive: true }));
  });

  it("closes access management and ignores its pending list when the conversation changes", async () => {
    const managedList = deferred<LocalWorkspaceView[]>();
    mocks.listWorkspaces.mockResolvedValueOnce([]).mockReturnValueOnce(managedList.promise);
    const view = render(<LocalWorkspaceControl onChange={vi.fn()} />);
    await waitFor(() => expect(mocks.listWorkspaces).toHaveBeenCalledTimes(1));

    fireEvent.click(screen.getByRole("button", { name: /chat\.workspace\.manage/ }));
    const title = await screen.findByText("chat.workspace.manageTitle");
    const dialog = title.closest("[role=dialog]");
    if (!dialog) throw new Error("workspace access dialog missing");
    await waitFor(() => expect(mocks.listWorkspaces).toHaveBeenCalledTimes(2));

    view.rerender(
      <LocalWorkspaceControl conversationId="conv-alpha" onChange={vi.fn()} />,
    );
    await act(async () => managedList.resolve([beta]));

    await waitFor(() => expect(dialog).toHaveClass("ant-zoom-leave"));
    expect(screen.queryByText(beta.path)).not.toBeInTheDocument();
  });

  it("reauthorizes an inactive grant without binding it to the draft", async () => {
    const revoked = { ...alpha, status: "revoked" as const };
    mocks.listWorkspaces.mockResolvedValueOnce([]).mockResolvedValueOnce([revoked]);
    mocks.prepareWorkspaceReauthorization.mockResolvedValue({ canceled: false, selection_token: "renew", display_name: "Alpha", path: alpha.path });
    const onChange = vi.fn();
    render(<LocalWorkspaceControl onChange={onChange} />);
    fireEvent.click(screen.getByRole("button", { name: /chat\.workspace\.manage/ }));
    fireEvent.click(await screen.findByRole("button", { name: "chat.workspace.reauthorize" }));
    expect(await screen.findByText("chat.workspace.authorizeTitle")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "chat.workspace.authorize" }));
    await waitFor(() => expect(mocks.authorizeWorkspace).toHaveBeenCalledWith("local", "renew"));
    expect(onChange).not.toHaveBeenCalled();
  });

  const approval = (status: WorkspaceApproval["status"] = "pending"): WorkspaceApproval => ({
    operation_id: "operation-1", path: "notes/draft.txt", operation: "replace", tool_name: "string_replace",
    task_id: "task-1", version: "observed-version", content_digest: "input-digest", status, expires_at: Date.now() + 300_000,
  });
  const respond = (items: WorkspaceApproval[]) => ({ data: { data: { items } } });
  const openRequests = async () => {
    mocks.getConversationWorkspace.mockResolvedValue(alpha);
    const view = render(<LocalWorkspaceControl conversationId="conv-alpha" onChange={vi.fn()} />);
    fireEvent.click(await screen.findByRole("button", { name: "chat.workspace.approval.open" }));
    expect(await screen.findByText("chat.workspace.approval.title")).toBeInTheDocument();
    return view;
  };
  const refreshRequests = async () => act(async () => { fireEvent(document, new Event("visibilitychange")); });

  it("continues one pending request through Core approval, execution and completion", async () => {
    let items = [approval()];
    vi.mocked(axiosInstance.get).mockImplementation(async () => respond(items));
    await openRequests();
    expect(await screen.findByText("notes/draft.txt")).toBeInTheDocument();
    expect(screen.getByText(/chat.workspace.approval.source.subagent/)).toHaveTextContent("task-1");
    expect(screen.getByText(/observed-version/)).toBeInTheDocument();
    expect(screen.getByText(/input-digest/)).toBeInTheDocument();
    items = [approval("allowed")];
    fireEvent.click(screen.getByRole("button", { name: "chat.workspace.approval.allowOnce" }));
    expect(await screen.findByText("chat.workspace.approval.status.allowed")).toBeInTheDocument();
    expect(axiosInstance.post).toHaveBeenCalledWith(
      "/api/core/conversations/conv-alpha/workspace-approvals/operation-1:decide", { action: "allow_once" },
    );
    expect(screen.queryByText("chat.workspace.approval.status.completed")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "chat.workspace.approval.allowOnce" })).not.toBeInTheDocument();
    items = [approval("executing")];
    await refreshRequests();
    expect(screen.getByText("chat.workspace.approval.status.executing")).toBeInTheDocument();
    items = [approval("completed")];
    await refreshRequests();
    expect(screen.getByText("chat.workspace.approval.status.completed")).toBeInTheDocument();
  });

  it("shows a rejected decision without treating it as execution", async () => {
    let items = [approval()];
    vi.mocked(axiosInstance.get).mockImplementation(async () => respond(items));
    vi.mocked(axiosInstance.post).mockImplementation(async () => {
      items = [approval("rejected")];
      return { data: { data: { status: "rejected" } } };
    });
    await openRequests();
    fireEvent.click(await screen.findByRole("button", { name: "chat.workspace.approval.reject" }));
    expect(await screen.findByText("chat.workspace.approval.status.rejected")).toBeInTheDocument();
    expect(axiosInstance.post).toHaveBeenCalledWith(
      "/api/core/conversations/conv-alpha/workspace-approvals/operation-1:decide", { action: "reject" },
    );
    expect(screen.queryByRole("button", { name: "chat.workspace.approval.allowOnce" })).not.toBeInTheDocument();
    expect(screen.queryByText("chat.workspace.approval.status.completed")).not.toBeInTheDocument();
  });

  it("shows a Core decision error and refreshes the authoritative expired status", async () => {
    let items = [approval()];
    vi.mocked(axiosInstance.get).mockImplementation(async () => respond(items));
    vi.mocked(axiosInstance.post).mockImplementation(async () => {
      items = [approval("expired")];
      throw { response: { data: { detail: { reason: "execution_inactive" } } } };
    });
    await openRequests();
    fireEvent.click(await screen.findByRole("button", { name: "chat.workspace.approval.allowOnce" }));
    expect(await screen.findByText("chat.workspace.approval.status.expired")).toBeInTheDocument();
    expect(message.error).toHaveBeenCalledWith(expect.stringContaining("chat.workspace.reason.execution_inactive"));
    expect(screen.queryByText("chat.workspace.approval.status.allowed")).not.toBeInTheDocument();
  });

  it.each(["approval_capacity", "operation_uncertain", "execution_inactive", "unsupported_file", "search_limit"])(
    "disables stale decisions and shows Core reason %s when refresh fails", async (reason) => {
      vi.mocked(axiosInstance.get).mockResolvedValue(respond([approval()]));
      await openRequests();
      expect(await screen.findByRole("button", { name: "chat.workspace.approval.allowOnce" })).toBeEnabled();
      vi.mocked(axiosInstance.get).mockRejectedValue({ response: { data: { data: { detail: { reason } } } } });
      await refreshRequests();
      expect(screen.getByRole("alert")).toHaveTextContent(`chat.workspace.reason.${reason}`);
      expect(screen.getByRole("button", { name: "chat.workspace.approval.allowOnce" })).toBeDisabled();
      expect(axiosInstance.post).not.toHaveBeenCalled();
    },
  );

  it("renders preparing and unknown-result states without approval actions", async () => {
    vi.mocked(axiosInstance.get).mockResolvedValue(respond([
      approval("preparing"), { ...approval("uncertain"), operation_id: "operation-2", task_id: "", attempt_id: "attempt-2", reason: "operation_uncertain" },
    ]));
    await openRequests();
    expect(await screen.findByText("chat.workspace.approval.status.preparing")).toBeInTheDocument();
    expect(screen.getByText("chat.workspace.approval.status.uncertain")).toBeInTheDocument();
    expect(screen.getByText("chat.workspace.approval.uncertain")).toBeInTheDocument();
    expect(screen.getByText(/chat.workspace.approval.source.workflow/)).toHaveTextContent("attempt-2");
    expect(screen.queryByRole("button", { name: "chat.workspace.approval.allowOnce" })).not.toBeInTheDocument();
  });

  it("clears busy decisions and discards old list and decision responses after switching conversations", async () => {
    const oldList = deferred<ReturnType<typeof respond>>();
    const oldDecision = deferred<{ data: { data: { status: string } } }>();
    const betaItem = { ...approval(), operation_id: "beta-operation", path: "beta.txt" };
    vi.mocked(axiosInstance.get).mockImplementation(async (url) => respond(String(url).includes("conv-beta") ? [betaItem] : [approval()]));
    vi.mocked(axiosInstance.post).mockReturnValueOnce(oldDecision.promise);
    const view = await openRequests();
    mocks.getConversationWorkspace.mockImplementation(async (id) => id === "conv-beta" ? beta : alpha);
    fireEvent.click(await screen.findByRole("button", { name: "chat.workspace.approval.allowOnce" }));
    vi.mocked(axiosInstance.get).mockReturnValueOnce(oldList.promise);
    await refreshRequests();
    view.rerender(<LocalWorkspaceControl conversationId="conv-beta" onChange={vi.fn()} />);
    expect(await screen.findByText(beta.path)).toBeInTheDocument();
    await act(async () => {
      oldList.resolve(respond([approval("completed")]));
      oldDecision.resolve({ data: { data: { status: "allowed" } } });
    });
    fireEvent.click(screen.getByRole("button", { name: "chat.workspace.approval.open" }));
    expect(await screen.findByText("beta.txt")).toBeInTheDocument();
    expect(screen.queryByText("notes/draft.txt")).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "chat.workspace.approval.allowOnce" })).toBeEnabled();
    expect(screen.queryByText("chat.workspace.approval.status.completed")).not.toBeInTheDocument();
  });

  it("stops polling and ignores hidden or unmounted replies, then refetches when visible", async () => {
    vi.mocked(axiosInstance.get).mockResolvedValue(respond([approval()]));
    const view = await openRequests();
    expect(await screen.findByText("notes/draft.txt")).toBeInTheDocument();
    vi.useFakeTimers();
    const hiddenResponse = deferred<ReturnType<typeof respond>>();
    vi.mocked(axiosInstance.get).mockReturnValueOnce(hiddenResponse.promise);
    await refreshRequests();
    const signal = vi.mocked(axiosInstance.get).mock.calls.slice(-1)[0]?.[1]?.signal;
    Object.defineProperty(document, "visibilityState", { configurable: true, value: "hidden" });
    await refreshRequests();
    expect(signal?.aborted).toBe(true);
    const callsWhenHidden = vi.mocked(axiosInstance.get).mock.calls.length;
    await act(async () => {
      hiddenResponse.resolve(respond([approval("completed")]));
      await vi.advanceTimersByTimeAsync(3000);
    });
    expect(axiosInstance.get).toHaveBeenCalledTimes(callsWhenHidden);
    expect(screen.queryByText("chat.workspace.approval.status.completed")).not.toBeInTheDocument();
    vi.mocked(axiosInstance.get).mockResolvedValue(respond([approval("allowed")]));
    Object.defineProperty(document, "visibilityState", { configurable: true, value: "visible" });
    await refreshRequests();
    expect(screen.getByText("chat.workspace.approval.status.allowed")).toBeInTheDocument();
    vi.mocked(axiosInstance.get).mockResolvedValue(respond([approval("executing")]));
    await act(async () => { await vi.advanceTimersByTimeAsync(1000); });
    expect(screen.getByText("chat.workspace.approval.status.executing")).toBeInTheDocument();
    const unmountedResponse = deferred<ReturnType<typeof respond>>();
    vi.mocked(axiosInstance.get).mockReturnValueOnce(unmountedResponse.promise);
    await refreshRequests();
    const lastSignal = vi.mocked(axiosInstance.get).mock.calls.slice(-1)[0]?.[1]?.signal;
    view.unmount();
    expect(lastSignal?.aborted).toBe(true);
    const callsAtUnmount = vi.mocked(axiosInstance.get).mock.calls.length;
    await act(async () => {
      unmountedResponse.resolve(respond([approval("completed")]));
      await vi.advanceTimersByTimeAsync(3000);
    });
    expect(axiosInstance.get).toHaveBeenCalledTimes(callsAtUnmount);
  });

});
