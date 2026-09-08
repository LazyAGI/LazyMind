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

vi.mock("@/modules/chat/utils/localWorkspace", () => ({
  authorizeWorkspace: mocks.authorizeWorkspace,
  getConversationWorkspace: mocks.getConversationWorkspace,
  listWorkspaces: mocks.listWorkspaces,
  prepareWorkspaceReauthorization: mocks.prepareWorkspaceReauthorization,
  revokeWorkspace: mocks.revokeWorkspace,
  selectWorkspaceCandidate: mocks.selectWorkspaceCandidate,
  updateWorkspacePermission: mocks.updateWorkspacePermission,
  workspaceReason: () => "workspace_error",
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
  const dialog = confirmTitle?.closest("[role=dialog]");
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
});
