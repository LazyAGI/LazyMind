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
import { Modal } from "antd";
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
    expect(screen.getByText("chat.workspace.askAsNeeded")).toBeInTheDocument();
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

  it("does not display a workspace request entry in a bound conversation", async () => {
    mocks.getConversationWorkspace.mockResolvedValue(alpha);
    render(<LocalWorkspaceControl conversationId="conv-alpha" onChange={vi.fn()} />);
    await waitFor(() => expect(screen.getByRole("combobox")).toBeEnabled());
    expect(screen.queryByText(/chat\.workspace\.approval\.open/)).not.toBeInTheDocument();
  });

  it("closes the approval dialog after its final pending operation is approved", async () => {
    mocks.getConversationWorkspace.mockResolvedValue(alpha);
    const pending = {
      operation_id: "operation-1", path: "notes/draft.txt", operation: "replace", status: "pending", expires_at: Date.now() + 60_000,
    };
    vi.mocked(axiosInstance.get).mockResolvedValue({ data: { data: { items: [pending] } } });
    render(<LocalWorkspaceControl conversationId="conv-alpha" onChange={vi.fn()} />);

    const dialog = (await screen.findByText("notes/draft.txt")).closest<HTMLElement>("[role=dialog]");
    if (!dialog) throw new Error("approval dialog missing");
    fireEvent.click(within(dialog).getByRole("button", { name: "chat.workspace.approval.allowOnce" }));

    await waitFor(() => expect(vi.mocked(axiosInstance.post)).toHaveBeenCalledWith(
      "/api/core/conversations/conv-alpha/workspace-approvals/operation-1:decide",
      { action: "allow_once" },
    ));
    await waitFor(() => expect(dialog).toHaveClass("ant-zoom-leave"));

    await act(async () => { document.dispatchEvent(new Event("visibilitychange")); });
    expect(dialog).toHaveClass("ant-zoom-leave");
  });

  it("keeps the approval dialog open while another operation is pending", async () => {
    mocks.getConversationWorkspace.mockResolvedValue(alpha);
    const pending = (id: string, path: string) => ({
      operation_id: id, path, operation: "replace", status: "pending", expires_at: Date.now() + 60_000,
    });
    vi.mocked(axiosInstance.get).mockResolvedValue({ data: { data: { items: [
      pending("operation-1", "first.txt"), pending("operation-2", "second.txt"),
    ] } } });
    render(<LocalWorkspaceControl conversationId="conv-alpha" onChange={vi.fn()} />);

    const dialog = (await screen.findByText("first.txt")).closest<HTMLElement>("[role=dialog]");
    if (!dialog) throw new Error("approval dialog missing");
    fireEvent.click(within(dialog).getAllByRole("button", { name: "chat.workspace.approval.allowOnce" })[0]);

    await waitFor(() => expect(vi.mocked(axiosInstance.post)).toHaveBeenCalled());
    expect(dialog).not.toHaveClass("ant-zoom-leave");
    expect(within(dialog).getByText("second.txt")).toBeInTheDocument();
  });

  it("does not reopen a dismissed approval until a new operation arrives", async () => {
    mocks.getConversationWorkspace.mockResolvedValue(alpha);
    const pending = (id: string, path: string) => ({
      operation_id: id, path, operation: "replace", status: "pending", expires_at: Date.now() + 60_000,
    });
    vi.mocked(axiosInstance.get).mockResolvedValue({ data: { data: { items: [pending("operation-1", "first.txt")] } } });
    render(<LocalWorkspaceControl conversationId="conv-alpha" onChange={vi.fn()} />);

    const dialog = (await screen.findByText("first.txt")).closest<HTMLElement>("[role=dialog]");
    if (!dialog) throw new Error("approval dialog missing");
    fireEvent.click(within(dialog).getByRole("button", { name: "Close" }));
    await waitFor(() => expect(dialog).toHaveClass("ant-zoom-leave"));

    const calls = vi.mocked(axiosInstance.get).mock.calls.length;
    await act(async () => { document.dispatchEvent(new Event("visibilitychange")); });
    await waitFor(() => expect(vi.mocked(axiosInstance.get).mock.calls.length).toBeGreaterThan(calls));
    expect(dialog).toHaveClass("ant-zoom-leave");

    vi.mocked(axiosInstance.get).mockResolvedValue({ data: { data: { items: [pending("operation-2", "second.txt")] } } });
    await act(async () => { document.dispatchEvent(new Event("visibilitychange")); });
    expect(await screen.findByText("second.txt")).toBeInTheDocument();
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

  it("opens approval only when an operation actually needs confirmation", async () => {
    mocks.getConversationWorkspace.mockResolvedValue(alpha);
    vi.mocked(axiosInstance.get).mockResolvedValue({ data: { data: { items: [{
      operation_id: "operation-1", path: "notes/draft.txt", operation: "replace", status: "pending", expires_at: Date.now() + 60_000,
    }] } } });
    render(<LocalWorkspaceControl conversationId="conv-alpha" onChange={vi.fn()} />);

    expect(await screen.findByText("notes/draft.txt")).toBeInTheDocument();
    expect(screen.queryByText(/chat\.workspace\.approval\.open/)).not.toBeInTheDocument();
  });

});
