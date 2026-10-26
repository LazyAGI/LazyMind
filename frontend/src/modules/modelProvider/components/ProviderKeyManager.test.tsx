import { useState } from "react";
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import {
  ProviderKeyManager,
  type ProviderKeyManagerProps,
} from "./ProviderKeyManager";

const mocks = vi.hoisted(() => ({
  reveal: vi.fn(),
  replace: vi.fn(),
  copy: vi.fn(),
}));
vi.mock("../api", () => ({
  revealProviderKey: mocks.reveal,
  replaceProviderKey: mocks.replace,
}));
vi.mock("react-i18next", () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}));
vi.mock("@/components/request", () => ({
  getLocalizedErrorMessage: (error: Error) => error.message,
}));

function props(): ProviderKeyManagerProps {
  return {
    providerId: "supplier",
    providerName: "Supplier",
    groupId: "storage",
    keys: [{ id: "key", masked: "********1234", enabled: true }],
    onAdd: vi.fn(),
    onDelete: vi.fn().mockResolvedValue(undefined),
    onToggle: vi.fn().mockResolvedValue(undefined),
    onReload: vi.fn().mockResolvedValue(undefined),
  };
}
beforeEach(() => {
  vi.clearAllMocks();
  mocks.reveal.mockResolvedValue("secret-for-explicit-action");
  mocks.replace.mockResolvedValue({ key_id: "replacement", enabled: true });
  mocks.copy.mockResolvedValue(undefined);
  Object.defineProperty(navigator, "clipboard", {
    value: { writeText: mocks.copy },
    configurable: true,
  });
});

describe("provider key controls", () => {
  it("keeps the API key heading free of a count while showing the configured key", () => {
    render(<ProviderKeyManager {...props()} />);
    expect(screen.getByRole("heading", { name: "modelProvider.apiKeyLabel" })).toBeInTheDocument();
    expect(screen.getByText("****1234")).toBeInTheDocument();
  });

  it("opens management after inline creation gives an unconfigured supplier its first storage", async () => {
    const create = vi.fn().mockResolvedValue(true);
    function Harness() {
      const [groupId, setGroupId] = useState("");
      return <ProviderKeyManager {...props()} groupId={groupId}
        keys={groupId ? [{ id: "created-key", masked: "********5678", enabled: true }] : []}
        onCreate={async (value) => {
          await create(value);
          setGroupId("created-storage");
          return true;
        }} />;
    }
    render(<Harness />);
    const input = screen.getByLabelText("modelProvider.apiKeyLabel");
    fireEvent.change(input, { target: { value: "fixture-first-key" } });
    fireEvent.blur(input);
    fireEvent.click(screen.getByRole("button", { name: "modelProvider.keyManager.manage" }));
    const drawer = within(await screen.findByRole("dialog"));
    expect(await drawer.findByText("****5678")).toBeVisible();
    await act(async () => {});
    expect(document.querySelector(".provider-key-manager-drawer")?.closest(".ant-drawer")).toHaveClass("ant-drawer-open");
    expect(create).toHaveBeenCalledTimes(1);
  });
  it.each(["enter", "blur"])("saves a trimmed first key directly on %s without opening management", async (action) => {
    const current = { ...props(), keys: [], onCreate: vi.fn().mockResolvedValue(true) };
    render(<ProviderKeyManager {...current} />);
    const input = screen.getByLabelText("modelProvider.apiKeyLabel");
    expect(input).toHaveAttribute("type", "password");
    fireEvent.change(input, { target: { value: "  fixture-inline-key  " } });
    if (action === "enter") fireEvent.keyDown(input, { key: "Enter", code: "Enter" });
    else fireEvent.blur(input);
    await waitFor(() => expect(current.onCreate).toHaveBeenCalledWith("fixture-inline-key"));
    await waitFor(() => expect(input).toHaveValue(""));
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });
  it("ignores blank values and rejects whitespace within a key", async () => {
    const current = { ...props(), keys: [], onCreate: vi.fn().mockResolvedValue(true) };
    render(<ProviderKeyManager {...current} />);
    const input = screen.getByLabelText("modelProvider.apiKeyLabel");
    fireEvent.change(input, { target: { value: "  " } });
    fireEvent.blur(input);
    fireEvent.change(input, { target: { value: "fixture key" } });
    fireEvent.keyDown(input, { key: "Enter", code: "Enter" });
    expect(await screen.findByRole("alert")).toHaveTextContent("modelProvider.validation.apiKeyNoSpaces");
    expect(current.onCreate).not.toHaveBeenCalled();
  });
  it("coalesces Enter and blur into one pending save and blocks a concurrent connection check", async () => {
    let finish!: (saved: boolean) => void;
    const check = vi.fn();
    const current = { ...props(), keys: [], onCreate: vi.fn().mockReturnValue(new Promise<boolean>((resolve) => { finish = resolve; })) };
    render(<ProviderKeyManager {...current} checkAction={<button onClick={check}>Check</button>} />);
    const input = screen.getByLabelText("modelProvider.apiKeyLabel");
    fireEvent.change(input, { target: { value: "fixture-inline-key" } });
    fireEvent.keyDown(input, { key: "Enter", code: "Enter" });
    fireEvent.blur(input);
    fireEvent.click(screen.getByRole("button", { name: "Check" }));
    await waitFor(() => expect(current.onCreate).toHaveBeenCalledTimes(1));
    expect(check).not.toHaveBeenCalled();
    expect(input).toBeDisabled();
    await act(async () => finish(true));
    expect(input).toHaveValue("");
  });
  it("keeps an unsuccessful draft and lets the user retry", async () => {
    const current = { ...props(), keys: [], onCreate: vi.fn().mockRejectedValueOnce(new Error("Could not save key")).mockResolvedValueOnce(true) };
    render(<ProviderKeyManager {...current} />);
    const input = screen.getByLabelText("modelProvider.apiKeyLabel");
    fireEvent.change(input, { target: { value: "fixture-inline-key" } });
    fireEvent.blur(input);
    expect(await screen.findByRole("alert")).toHaveTextContent("Could not save key");
    expect(input).toHaveValue("fixture-inline-key");
    fireEvent.keyDown(input, { key: "Enter", code: "Enter" });
    await waitFor(() => expect(input).toHaveValue(""));
    expect(current.onCreate).toHaveBeenCalledTimes(2);
  });
  it("keeps a declined draft without resubmitting until another explicit action", async () => {
    const current = { ...props(), keys: [], onCreate: vi.fn().mockResolvedValue(false) };
    render(<ProviderKeyManager {...current} />);
    const input = screen.getByLabelText("modelProvider.apiKeyLabel");
    fireEvent.change(input, { target: { value: "fixture-inline-key" } });
    fireEvent.blur(input);
    await waitFor(() => expect(input).not.toBeDisabled());
    expect(input).toHaveValue("fixture-inline-key");
    expect(current.onCreate).toHaveBeenCalledTimes(1);
  });
  it("discards a first-key draft on supplier change and ignores its stale result", async () => {
    let finish!: (saved: boolean) => void;
    const current = { ...props(), keys: [], onCreate: vi.fn().mockReturnValue(new Promise<boolean>((resolve) => { finish = resolve; })) };
    const nextCreate = vi.fn().mockResolvedValue(true);
    const view = render(<ProviderKeyManager {...current} />);
    const input = screen.getByLabelText("modelProvider.apiKeyLabel");
    fireEvent.change(input, { target: { value: "old-supplier-key" } });
    fireEvent.blur(input);
    await waitFor(() => expect(current.onCreate).toHaveBeenCalledTimes(1));
    view.rerender(<ProviderKeyManager {...current} providerId="next-supplier" groupId="next-storage" onCreate={nextCreate} />);
    expect(screen.getByLabelText("modelProvider.apiKeyLabel")).toHaveValue("");
    await act(async () => finish(true));
    expect(screen.getByLabelText("modelProvider.apiKeyLabel")).toHaveValue("");
    expect(nextCreate).not.toHaveBeenCalled();
  });
  it.each([
    ["********...ABCD", "****ABCD"],
    ["********1234", "****1234"],
    ["********", "****"],
  ])("shows a compact masked preview without a numbered row label: %s", (masked, preview) => {
    render(<ProviderKeyManager {...props()} keys={[{ id: "key", masked, enabled: true }]} />);
    expect(screen.getByText(preview)).toBeVisible();
    expect(screen.queryByText("modelProvider.workspace.keyNumber")).not.toBeInTheDocument();
    expect(mocks.reveal).not.toHaveBeenCalled();
  });
  it("adds keys through a single management entry, including an empty supplier", async () => {
    const current = { ...props(), keys: [] };
    render(<ProviderKeyManager {...current} />);
    expect(
      screen.queryByRole("button", { name: "modelProvider.workspace.addKey" }),
    ).not.toBeInTheDocument();
    fireEvent.click(
      screen.getByRole("button", { name: "modelProvider.keyManager.manage" }),
    );
    const drawer = within(await screen.findByRole("dialog"));
    fireEvent.click(
      drawer.getByRole("button", { name: "modelProvider.workspace.addKey" }),
    );
    expect(current.onAdd).toHaveBeenCalledTimes(1);
  });
  it("only reveals on explicit action and hides on a supplier change", async () => {
    const initial = props();
    const view = render(<ProviderKeyManager {...initial} />);
    expect(screen.getByText("****1234")).toBeInTheDocument();
    expect(mocks.reveal).not.toHaveBeenCalled();
    fireEvent.click(
      screen.getByRole("button", { name: "modelProvider.keyManager.reveal" }),
    );
    expect(
      await screen.findByText("secret-for-explicit-action"),
    ).toBeInTheDocument();
    expect(mocks.reveal).toHaveBeenCalledWith("supplier", "storage", "key");
    view.rerender(<ProviderKeyManager {...initial} providerId="another" />);
    expect(
      screen.queryByText("secret-for-explicit-action"),
    ).not.toBeInTheDocument();
  });
  it("copies the actual key without displaying it", async () => {
    render(<ProviderKeyManager {...props()} />);
    fireEvent.click(
      screen.getByRole("button", { name: "modelProvider.keyManager.copy" }),
    );
    await waitFor(() =>
      expect(mocks.copy).toHaveBeenCalledWith("secret-for-explicit-action"),
    );
    expect(
      screen.queryByText("secret-for-explicit-action"),
    ).not.toBeInTheDocument();
  });
  it("replaces a key and refreshes the supplier without automatically toggling it", async () => {
    const current = props();
    render(<ProviderKeyManager {...current} />);
    fireEvent.click(
      screen.getByRole("button", { name: "modelProvider.keyManager.edit" }),
    );
    const dialog = within(await screen.findByRole("dialog"));
    fireEvent.change(dialog.getByLabelText("modelProvider.apiKeyLabel"), {
      target: { value: "replacement-secret" },
    });
    fireEvent.click(dialog.getByRole("button", { name: "common.save" }));
    await waitFor(() =>
      expect(mocks.replace).toHaveBeenCalledWith(
        "supplier",
        "storage",
        "key",
        "replacement-secret",
      ),
    );
    await waitFor(() => expect(current.onReload).toHaveBeenCalled());
    expect(current.onToggle).not.toHaveBeenCalled();
  });
  it("opens management and routes a saved key selection through the single-key callback", async () => {
    const current = props();
    current.keys.push({ id: "other", masked: "********5678", enabled: false });
    render(<ProviderKeyManager {...current} />);
    fireEvent.click(
      screen.getByRole("button", { name: "modelProvider.keyManager.manage" }),
    );
    const drawer = within(await screen.findByRole("dialog"));
    fireEvent.click(drawer.getAllByRole("switch")[1]);
    await waitFor(() =>
      expect(current.onToggle).toHaveBeenCalledWith("other", true),
    );
  });
  it("drops an in-flight reveal when the key drawer closes", async () => {
    let resolve!: (value: string) => void;
    mocks.reveal.mockReturnValue(new Promise<string>((done) => { resolve = done; }));
    render(<ProviderKeyManager {...props()} />);
    fireEvent.click(screen.getByRole("button", { name: "modelProvider.keyManager.manage" }));
    const drawer = within(await screen.findByRole("dialog"));
    fireEvent.click(drawer.getByRole("button", { name: "modelProvider.keyManager.reveal" }));
    fireEvent.click(drawer.getByRole("button", { name: "Close" }));
    await act(async () => resolve("late-drawer-secret"));
    expect(screen.queryByText("late-drawer-secret")).not.toBeInTheDocument();
    mocks.reveal.mockResolvedValueOnce("main-page-secret");
    fireEvent.click(screen.getByRole("button", { name: "modelProvider.keyManager.reveal" }));
    expect(await screen.findByText("main-page-secret")).toBeInTheDocument();
  });
  it("does not expose a stale reveal response after switching suppliers", async () => {
    let resolve!: (value: string) => void;
    mocks.reveal.mockReturnValue(
      new Promise<string>((done) => {
        resolve = done;
      }),
    );
    const current = props();
    const view = render(<ProviderKeyManager {...current} />);
    fireEvent.click(
      screen.getByRole("button", { name: "modelProvider.keyManager.reveal" }),
    );
    view.rerender(<ProviderKeyManager {...current} providerId="another" />);
    await act(async () => resolve("old-supplier-secret"));
    expect(screen.queryByText("old-supplier-secret")).not.toBeInTheDocument();
  });
});
