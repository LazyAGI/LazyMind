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
import { message, Modal } from "antd";
import { axiosInstance } from "@/components/request";
import type { LocalWorkspaceView } from "@/modules/chat/utils/localWorkspace";
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
  await openWorkspacePicker();
  return screen.findByRole("dialog");
}

async function openWorkspaceMenu() {
  fireEvent.click(screen.getByRole("button", { name: /chat\.workspace\.select/ }));
  return screen.findByPlaceholderText("chat.workspace.searchShort");
}

async function openWorkspacePicker() {
  await openWorkspaceMenu();
  fireEvent.click(screen.getByRole("button", { name: /chat\.workspace\.openFolder/ }));
}

async function openWorkspaceManager() {
  await openWorkspaceMenu();
  fireEvent.click(screen.getByRole("button", { name: /chat\.workspace\.manage/ }));
}

async function findConfirmDialog(title: string) {
  const titles = await screen.findAllByText(title);
  const dialog = titles.map((item) => item.closest<HTMLElement>("[role=dialog]")).find(Boolean);
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
    Object.values(mocks).forEach((mock) => mock.mockReset());
    vi.mocked(axiosInstance.get).mockResolvedValue({ data: { data: { items: [] } } });
    vi.mocked(axiosInstance.post).mockResolvedValue({ data: { data: { status: "allowed" } } });
    Object.defineProperty(document, "visibilityState", { configurable: true, value: "visible" });
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

  it("resets a reused draft to no workspace and ask-as-needed", async () => {
    mocks.listWorkspaces.mockResolvedValue([alpha]);
    const onChange = vi.fn();
    const { rerender } = render(<LocalWorkspaceControl configResetKey={1} onChange={onChange} />);

    await openWorkspaceMenu();
    fireEvent.click(await screen.findByRole("button", { name: /Alpha/ }));
    expect(onChange).toHaveBeenLastCalledWith(alpha.workspace_id, "ask_as_needed");

    rerender(<LocalWorkspaceControl configResetKey={2} onChange={onChange} />);

    await waitFor(() => expect(onChange).toHaveBeenLastCalledWith(undefined, "ask_as_needed"));
    expect(screen.getByRole("combobox")).toBeDisabled();
  });

  it("locks folder selection for an existing bound task", async () => {
    mocks.getConversationWorkspace.mockResolvedValue(alpha);
    const onChange = vi.fn();

    render(<LocalWorkspaceControl conversationId="conv-alpha" onChange={onChange} />);

    await waitFor(() => expect(screen.getByRole("combobox")).toBeEnabled());
    expect(screen.queryByText(alpha.display_name)).not.toBeInTheDocument();
    expect(screen.queryByText(alpha.path)).not.toBeInTheDocument();
    expect(mocks.selectWorkspaceCandidate).not.toHaveBeenCalled();
  });

  it("does not allow a workspace to be added to an existing unbound task", async () => {
    const onChange = vi.fn();
    render(<LocalWorkspaceControl conversationId="conv-unbound" onChange={onChange} />);

    await waitFor(() => {
      expect(mocks.getConversationWorkspace).toHaveBeenCalledWith("conv-unbound");
    });
    expect(screen.queryByRole("button", { name: /chat\.workspace\.select/ })).not.toBeInTheDocument();
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
    await waitFor(() => expect(screen.getByRole("combobox")).toBeEnabled());
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
    await waitFor(() => expect(mocks.getConversationWorkspace).toHaveBeenCalledWith("conv-beta"));
    await act(async () => oldLookup.resolve(alpha));
    await waitFor(() => expect(screen.getByRole("combobox")).toBeEnabled());
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
    await openWorkspacePicker();
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
    await openWorkspacePicker();
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

    await openWorkspacePicker();
    await waitFor(() => expect(mocks.selectWorkspaceCandidate).toHaveBeenCalled());

    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(mocks.authorizeWorkspace).not.toHaveBeenCalled();
    expect(onChange).not.toHaveBeenCalled();
  });

  it("uses one workspace menu and keeps the permission mode beside it", async () => {
    mocks.listWorkspaces.mockResolvedValue([alpha]);
    render(<LocalWorkspaceControl onChange={vi.fn()} />);

    expect(await screen.findByRole("button", { name: /chat\.workspace\.select/ })).toBeInTheDocument();
    expect(screen.queryByText("chat.workspace.recent")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "chat.workspace.manage" })).not.toBeInTheDocument();
    expect(screen.getByText("chat.workspace.everyAsk")).toBeInTheDocument();
  });

  it("keeps a same-draft workspace list when the native picker is canceled", async () => {
    const list = deferred<LocalWorkspaceView[]>();
    mocks.listWorkspaces.mockReturnValue(list.promise);
    render(<LocalWorkspaceControl onChange={vi.fn()} />);

    await openWorkspacePicker();
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

    await openWorkspacePicker();
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
    await waitFor(() => expect(screen.getByRole("combobox")).toBeEnabled());
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
    await waitFor(() => expect(screen.getByRole("combobox")).toBeEnabled());
    fireEvent.mouseDown(screen.getByRole("combobox"));
    fireEvent.click(await screen.findByText("chat.workspace.everyAsk"));
    await waitFor(() => expect(mocks.updateWorkspacePermission).toHaveBeenCalled());

    view.rerender(
      <LocalWorkspaceControl conversationId="conv-beta" onChange={onChange} />,
    );
    await waitFor(() => expect(mocks.getConversationWorkspace).toHaveBeenCalledWith("conv-beta"));
    onChange.mockClear();
    await act(async () =>
      update.resolve({
        permission_mode: "always_ask",
        permission_version: 4,
        effective_at: "next_request",
      }),
    );

    expect(screen.queryByText(alpha.path)).not.toBeInTheDocument();
    expect(onChange).not.toHaveBeenCalled();
  });

  it("does not confirm allow-all for a conversation that is no longer current", async () => {
    mocks.getConversationWorkspace.mockImplementation((conversationId: string) =>
      Promise.resolve(conversationId === "conv-alpha" ? alpha : beta),
    );
    const view = render(
      <LocalWorkspaceControl conversationId="conv-alpha" onChange={vi.fn()} />,
    );
    await waitFor(() => expect(screen.getByRole("combobox")).toBeEnabled());
    fireEvent.mouseDown(screen.getByRole("combobox"));
    fireEvent.click(await screen.findByText("chat.workspace.allowAll"));
    const dialog = await findConfirmDialog("chat.workspace.allowAllTitle");

    view.rerender(
      <LocalWorkspaceControl conversationId="conv-beta" onChange={vi.fn()} />,
    );
    await waitFor(() => expect(mocks.getConversationWorkspace).toHaveBeenCalledWith("conv-beta"));
    fireEvent.click(within(dialog).getByRole("button", { name: "chat.workspace.allowAllConfirm" }));

    expect(mocks.updateWorkspacePermission).not.toHaveBeenCalled();
  });

  it("searches active and inactive grants from the access manager", async () => {
    mocks.listWorkspaces.mockResolvedValueOnce([]).mockResolvedValueOnce([alpha, { ...beta, status: "revoked" }]).mockResolvedValue([]);
    render(<LocalWorkspaceControl onChange={vi.fn()} />);
    await openWorkspaceManager();
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

    await openWorkspaceManager();
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
    await openWorkspaceManager();
    fireEvent.click(await screen.findByRole("button", { name: "chat.workspace.reauthorize" }));
    expect(await screen.findByText("chat.workspace.authorizeTitle")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "chat.workspace.authorize" }));
    await waitFor(() => expect(mocks.authorizeWorkspace).toHaveBeenCalledWith("local", "renew"));
    expect(onChange).not.toHaveBeenCalled();
  });

  it("restores the welcome composer selection without overriding later choices", async () => {
    mocks.listWorkspaces.mockResolvedValue([alpha, beta]);
    const onChange = vi.fn();
    render(<LocalWorkspaceControl draftWorkspace={{ workspace_id: alpha.workspace_id, workspace_permission_mode: "always_ask" }} onChange={onChange} />);
    await waitFor(() => expect(onChange).toHaveBeenCalledWith(alpha.workspace_id, "always_ask"));
    expect(screen.getByRole("button", { name: /Alpha/ })).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /Alpha/ }));
    fireEvent.click(await screen.findByRole("button", { name: /chat.workspace.none/ }));
    await waitFor(() => expect(onChange).toHaveBeenLastCalledWith(undefined, "always_ask"));
  });

  it.each([["execution_inactive", "status.inactive"], ["selection_expired", "requestExpired"]])("shows approval decision error %s", async (reason, label) => {
    mocks.getConversationWorkspace.mockResolvedValue(alpha);
    const operation = { operation_id: "ended", operation: "write", path: "/tmp/output", status: "pending", expires_at: Date.now() + 60000 };
    vi.mocked(axiosInstance.get).mockResolvedValue({ data: { data: { items: [operation] } } });
    vi.mocked(axiosInstance.post).mockRejectedValue({ response: { data: { detail: { reason } } } });
    render(<LocalWorkspaceControl conversationId="conv-alpha" onChange={vi.fn()} />);
    await screen.findByRole("region", { name: "chat.workspace.approval.title" });
    vi.mocked(axiosInstance.get).mockResolvedValue({ data: { data: { items: [{ ...operation, status: "expired", reason }] } } });
    fireEvent.click(await screen.findByRole("button", { name: "chat.workspace.approval.allowOnce" }));
    await waitFor(() => expect(message.error).toHaveBeenCalledWith(`chat.workspace.approval.decisionFailed：chat.workspace.approval.${label}`));
    await waitFor(() => expect(screen.queryByRole("button", { name: "chat.workspace.approval.allowOnce" })).not.toBeInTheDocument());
  });

  it.each([["execution_inactive", "inactive"], ["selection_expired", "expired"]])("distinguishes expired approval reason %s", async (reason, label) => {
    mocks.getConversationWorkspace.mockResolvedValue(alpha);
    const operation = { operation_id: "ended", operation: "write", path: "/tmp/output", status: "pending", expires_at: Date.now() + 60000 };
    vi.mocked(axiosInstance.get).mockResolvedValue({ data: { data: { items: [operation] } } });
    render(<LocalWorkspaceControl conversationId="conv-alpha" onChange={vi.fn()} />);
    await screen.findByRole("region", { name: "chat.workspace.approval.title" });
    vi.mocked(axiosInstance.get).mockResolvedValue({ data: { data: { items: [{ ...operation, status: "expired", reason }] } } });
    fireEvent(document, new Event("visibilitychange"));
    expect(await screen.findByText(`chat.workspace.approval.status.${label}`)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "chat.workspace.approval.allowOnce" })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "chat.workspace.approval.dismiss" }));
    expect(screen.queryByRole("region", { name: "chat.workspace.approval.title" })).not.toBeInTheDocument();
  });

  it("does not display a workspace request entry in a bound conversation", async () => {
    mocks.getConversationWorkspace.mockResolvedValue(alpha);
    render(<LocalWorkspaceControl conversationId="conv-alpha" onChange={vi.fn()} />);
    await waitFor(() => expect(screen.getByRole("combobox")).toBeEnabled());
    expect(screen.queryByText(/chat\.workspace\.approval\.open/)).not.toBeInTheDocument();
  });

  it.each([true, false])("shows generic tool approval actions from the frozen operation (future=%s)", async (allowFuture) => {
    mocks.getConversationWorkspace.mockResolvedValue(alpha);
    const pending = {
      operation_id: "tool-1", path: "", operation: "tool", capability: "tool",
      tool_name: "remote_echo", tool_origin: "Example MCP", allow_future: allowFuture,
      status: "pending", expires_at: Date.now() + 60_000,
    };
    vi.mocked(axiosInstance.get).mockResolvedValue({ data: { data: { items: [pending] } } });
    render(<LocalWorkspaceControl conversationId="conv-alpha" onChange={vi.fn()} />);
    const label = await screen.findByText("remote_echo · Example MCP");
    const dialog = label.closest<HTMLElement>("[role=region]");
    if (!dialog) throw new Error("approval card missing");
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(within(dialog).getByText("chat.workspace.approval.unknownFileAccess")).toBeInTheDocument();
    const future = within(dialog).queryByRole("button", { name: "chat.workspace.approval.allowFuture" });
    expect(Boolean(future)).toBe(allowFuture);
    fireEvent.click(future || within(dialog).getByRole("button", { name: "chat.workspace.approval.allowOnce" }));
    await waitFor(() => expect(vi.mocked(axiosInstance.post)).toHaveBeenCalledWith(
      "/api/core/conversations/conv-alpha/workspace-approvals/tool-1:decide", { action: allowFuture ? "allow_future" : "allow_once" },
    ));
  });

  it("allows future shell calls only for this conversation", async () => {
    mocks.getConversationWorkspace.mockResolvedValue(alpha);
    const pending = {
      operation_id: "shell-1", path: "", operation: "shell", capability: "shell", allow_future: true,
      command: "echo approved", status: "pending", expires_at: Date.now() + 60_000,
    };
    vi.mocked(axiosInstance.get).mockResolvedValue({ data: { data: { items: [pending] } } });
    render(<LocalWorkspaceControl conversationId="conv-alpha" onChange={vi.fn()} />);
    const command = await screen.findByText("echo approved");
    const dialog = command.closest<HTMLElement>("[role=region]");
    if (!dialog) throw new Error("approval card missing");
    fireEvent.click(within(dialog).getByRole("button", { name: "chat.workspace.approval.allowFuture" }));
    await waitFor(() => expect(vi.mocked(axiosInstance.post)).toHaveBeenCalledWith(
      "/api/core/conversations/conv-alpha/workspace-approvals/shell-1:decide", { action: "allow_future" },
    ));
  });

  it("removes the approval card after its final pending operation is approved", async () => {
    mocks.getConversationWorkspace.mockResolvedValue(alpha);
    const pending = {
      operation_id: "operation-1", path: "notes/draft.txt", operation: "replace", status: "pending", expires_at: Date.now() + 60_000,
    };
    vi.mocked(axiosInstance.get).mockResolvedValue({ data: { data: { items: [pending] } } });
    render(<LocalWorkspaceControl conversationId="conv-alpha" onChange={vi.fn()} />);

    const dialog = (await screen.findByText("notes/draft.txt")).closest<HTMLElement>("[role=region]");
    if (!dialog) throw new Error("approval card missing");
    fireEvent.click(within(dialog).getByRole("button", { name: "chat.workspace.approval.allowOnce" }));

    await waitFor(() => expect(vi.mocked(axiosInstance.post)).toHaveBeenCalledWith(
      "/api/core/conversations/conv-alpha/workspace-approvals/operation-1:decide",
      { action: "allow_once" },
    ));
    await waitFor(() => expect(dialog).not.toBeInTheDocument());

    await act(async () => { document.dispatchEvent(new Event("visibilitychange")); });
    expect(dialog).not.toBeInTheDocument();
  });

  it("shows the next approval while another operation is pending", async () => {
    mocks.getConversationWorkspace.mockResolvedValue(alpha);
    const pending = (id: string, path: string) => ({
      operation_id: id, path, operation: "replace", status: "pending", expires_at: Date.now() + 60_000,
    });
    vi.mocked(axiosInstance.get).mockResolvedValue({ data: { data: { items: [
      pending("operation-1", "first.txt"), pending("operation-2", "second.txt"),
    ] } } });
    render(<LocalWorkspaceControl conversationId="conv-alpha" onChange={vi.fn()} />);

    const dialog = (await screen.findByText("first.txt")).closest<HTMLElement>("[role=region]");
    if (!dialog) throw new Error("approval card missing");
    fireEvent.click(within(dialog).getAllByRole("button", { name: "chat.workspace.approval.allowOnce" })[0]);

    await waitFor(() => expect(vi.mocked(axiosInstance.post)).toHaveBeenCalled());
    expect(await screen.findByText("second.txt")).toBeInTheDocument();
    expect(screen.queryByText("first.txt")).not.toBeInTheDocument();
  });

  it.each([undefined, "conv-alpha"])("skips workspace requests outside local runtimes (%s)", async (conversationId) => {
    mocks.getRuntimeMode.mockReturnValue("cloud");
    const { rerender } = render(<LocalWorkspaceControl conversationId={conversationId} onChange={vi.fn()} />);
    await act(async () => {});
    expect(mocks.getConversationWorkspace).not.toHaveBeenCalled();
    expect(mocks.listWorkspaces).not.toHaveBeenCalled();
    mocks.getRuntimeMode.mockReturnValue("local");
    rerender(<LocalWorkspaceControl conversationId={conversationId} onChange={vi.fn()} />);
    await waitFor(() => expect(conversationId ? mocks.getConversationWorkspace : mocks.listWorkspaces).toHaveBeenCalled());
  });

  it("retries a failed binding lookup without changing conversations and restores approvals", async () => {
    mocks.getConversationWorkspace.mockRejectedValueOnce(new Error("offline")).mockResolvedValue(alpha);
    vi.mocked(axiosInstance.get).mockResolvedValue({ data: { data: { items: [{
      operation_id: "retry-op", path: "recovered.txt", operation: "replace", status: "pending", expires_at: Date.now() + 60_000,
    }] } } });
    render(<LocalWorkspaceControl conversationId="conv-alpha" onChange={vi.fn()} />);
    fireEvent.click(await screen.findByRole("button", { name: "chat.workspace.retry" }));
    expect(await screen.findByRole("combobox")).toBeEnabled();
    expect(await screen.findByText("recovered.txt")).toBeInTheDocument();
    expect(mocks.getConversationWorkspace).toHaveBeenCalledTimes(2);
  });

  it("keeps the existing binding when a conflict refresh fails, then retries in place", async () => {
    mocks.getConversationWorkspace.mockResolvedValue(alpha);
    mocks.updateWorkspacePermission.mockRejectedValueOnce({
      response: { data: { code: "binding_conflict" } },
    });
    mocks.getConversationWorkspace
      .mockResolvedValueOnce(alpha)
      .mockRejectedValueOnce(new Error("offline"))
      .mockResolvedValueOnce({ ...alpha, permission_mode: "always_ask", permission_version: 4 });

    const onChange = vi.fn();
    render(<LocalWorkspaceControl conversationId="conv-alpha" onChange={onChange} />);
    await waitFor(() => expect(screen.getByRole("combobox")).toBeEnabled());
    onChange.mockClear();

    fireEvent.mouseDown(screen.getByRole("combobox"));
    fireEvent.click(await screen.findByText("chat.workspace.everyAsk"));

    await waitFor(() => expect(mocks.updateWorkspacePermission).toHaveBeenCalled());
    expect(await screen.findByRole("button", { name: "chat.workspace.retry" })).toBeInTheDocument();
    expect(screen.getByRole("combobox")).toBeEnabled();
    expect(onChange).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole("button", { name: "chat.workspace.retry" }));

    await waitFor(() => expect(onChange).toHaveBeenLastCalledWith(alpha.workspace_id, "always_ask"));
    expect(screen.queryByRole("button", { name: "chat.workspace.retry" })).not.toBeInTheDocument();
    expect(mocks.getConversationWorkspace).toHaveBeenCalledTimes(3);
    vi.mocked(axiosInstance.get).mockResolvedValue({ data: { data: { items: [{
      operation_id: "conflict-retry-op", path: "recovered.txt", operation: "replace", status: "pending", expires_at: Date.now() + 60_000,
    }] } } });
    await act(async () => { document.dispatchEvent(new Event("visibilitychange")); });
    expect(await screen.findByText("recovered.txt")).toBeInTheDocument();
  });

  it("reports saving until a delayed allow_all to always_ask update settles", async () => {
    mocks.getConversationWorkspace.mockResolvedValue({ ...alpha, permission_mode: "allow_all" });
    const update = deferred<{ permission_mode: "always_ask"; permission_version: number; effective_at: string }>();
    mocks.updateWorkspacePermission.mockReturnValue(update.promise);
    const onSavingChange = vi.fn();
    render(<LocalWorkspaceControl conversationId="conv-alpha" onChange={vi.fn()} onSavingChange={onSavingChange} />);
    fireEvent.mouseDown(await screen.findByRole("combobox"));
    fireEvent.click((await screen.findAllByText("chat.workspace.everyAsk"))[0]);
    await waitFor(() => expect(mocks.updateWorkspacePermission).toHaveBeenCalled());
    expect(onSavingChange).toHaveBeenLastCalledWith(true);
    await act(async () => update.resolve({ permission_mode: "always_ask", permission_version: 4, effective_at: "next_request" }));
    expect(onSavingChange).toHaveBeenLastCalledWith(false);
  });

  it("renders approvals above the composer without losing its draft or focus", async () => {
    const pending = { operation_id: "inline-1", path: "draft.txt", operation: "write", status: "pending", expires_at: Date.now() + 60_000 };
    const composer = render(<><div data-testid="approval-slot" /><textarea aria-label="Draft" defaultValue="Keep my draft" /></>);
    const draft = screen.getByRole("textbox", { name: "Draft" });
    draft.focus();
    vi.mocked(axiosInstance.get).mockResolvedValue({ data: { data: { items: [pending] } } });
    render(<LocalWorkspaceControl approvalContainer={screen.getByTestId("approval-slot")} conversationId="conv-alpha" onChange={vi.fn()} />);
    const card = await within(screen.getByTestId("approval-slot")).findByRole("region", { name: "chat.workspace.approval.title" });
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(draft).toHaveFocus();
    expect(draft).toHaveValue("Keep my draft");
    vi.mocked(axiosInstance.get).mockResolvedValue({ data: { data: { items: [] } } });
    vi.mocked(axiosInstance.post).mockResolvedValue({ data: { data: { status: "rejected" } } });
    fireEvent.click(within(card).getByRole("button", { name: "chat.workspace.approval.reject" }));
    await waitFor(() => expect(card).not.toBeInTheDocument());
    expect(axiosInstance.post).toHaveBeenCalledWith("/api/core/conversations/conv-alpha/workspace-approvals/inline-1:decide", { action: "reject" });
    expect(draft).toHaveValue("Keep my draft");
    composer.unmount();
  });

  it("removes the previous conversation's inline approval when switching conversations", async () => {
    vi.mocked(axiosInstance.get).mockResolvedValue({ data: { data: { items: [{
      operation_id: "operation-1", path: "first.txt", operation: "replace", status: "pending", expires_at: Date.now() + 60_000,
    }] } } });
    const view = render(<LocalWorkspaceControl conversationId="conv-alpha" onChange={vi.fn()} />);
    await screen.findByText("first.txt");
    vi.mocked(axiosInstance.get).mockResolvedValue({ data: { data: { items: [] } } });
    view.rerender(<LocalWorkspaceControl conversationId="conv-beta" onChange={vi.fn()} />);
    expect(screen.queryByText("first.txt")).not.toBeInTheDocument();
    expect(screen.queryByRole("region", { name: "chat.workspace.approval.title" })).not.toBeInTheDocument();
  });

  it("cancels a stale revoke confirmation when the component lifetime changes", async () => {
    mocks.listWorkspaces.mockResolvedValue([alpha]);
    let confirm: (() => Promise<void>) | undefined;
    const destroy = vi.fn();
    vi.spyOn(Modal, "confirm").mockImplementation(((config: { onOk?: () => Promise<void> }) => {
      confirm = config.onOk;
      return { destroy, update: vi.fn() };
    }) as typeof Modal.confirm);
    const { rerender } = render(<LocalWorkspaceControl configResetKey={1} onChange={vi.fn()} />);
    await openWorkspaceManager();
    fireEvent.click(await screen.findByRole("button", { name: "chat.workspace.revoke" }));

    rerender(<LocalWorkspaceControl configResetKey={2} onChange={vi.fn()} />);
    expect(destroy).toHaveBeenCalled();
    await confirm?.();
    expect(mocks.revokeWorkspace).not.toHaveBeenCalled();
  });

  it("opens approval for an unbound conversation when confirmation is needed", async () => {
    mocks.getConversationWorkspace.mockResolvedValue(undefined);
    vi.mocked(axiosInstance.get).mockResolvedValue({ data: { data: { items: [{
      operation_id: "operation-1", path: "notes/draft.txt", operation: "replace", status: "pending", expires_at: Date.now() + 60_000,
    }] } } });
    render(<LocalWorkspaceControl conversationId="conv-alpha" onChange={vi.fn()} />);

    expect(await screen.findByText("notes/draft.txt")).toBeInTheDocument();
    expect(screen.getByRole("region", { name: "chat.workspace.approval.title" })).toBeInTheDocument();
  });

});
