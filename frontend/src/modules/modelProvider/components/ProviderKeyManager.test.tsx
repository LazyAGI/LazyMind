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
