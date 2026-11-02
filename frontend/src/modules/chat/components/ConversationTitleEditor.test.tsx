import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { StrictMode } from "react";
import ConversationTitleEditor from "./ConversationTitleEditor";
import { renameGroupConversation, emitConversationGroupsChanged } from "../conversationOrganizer/api";

const mocks = vi.hoisted(() => ({ detail: vi.fn() }));
const t = (key: string) => key;
vi.mock("react-i18next", () => ({ useTranslation: () => ({ t }) }));
vi.mock("../utils/request", () => ({ ChatServiceApi: () => ({ conversationServiceGetConversationDetail: mocks.detail }) }));
vi.mock("../conversationOrganizer/api", () => ({ renameGroupConversation: vi.fn(), emitConversationGroupsChanged: vi.fn() }));

beforeEach(() => {
  vi.clearAllMocks();
  mocks.detail.mockResolvedValue({ data: { conversation: { display_name: "Original", title_revision: 4 } } });
  vi.mocked(renameGroupConversation).mockResolvedValue({ display_name: "Custom", title_revision: 5 });
});

describe("conversation renaming", () => {
  it("opens a dialog, selects the current name, and cancels with Escape without activating its row", async () => {
    const close = vi.fn();
    const activate = vi.fn();
    render(<div onClick={activate} onKeyDown={activate}>
      <ConversationTitleEditor conversationId="chat" onClose={close} />
    </div>);
    const input = await screen.findByDisplayValue("Original") as HTMLInputElement;
    expect(screen.getByRole("dialog", { name: "conversationOrganizer.renameConversation" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "common.save" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "common.cancel" })).toBeInTheDocument();
    // The loaded value can appear before the focus/select effect has run.
    await waitFor(() => {
      expect(input).toHaveFocus();
      expect(input.selectionStart).toBe(0);
      expect(input.selectionEnd).toBe("Original".length);
    });
    fireEvent.click(input);
    fireEvent.change(input, { target: { value: "Draft" } });
    fireEvent.keyDown(input, { key: "Escape" });
    fireEvent.blur(input);
    expect(close).toHaveBeenCalledOnce();
    expect(activate).not.toHaveBeenCalled();
    expect(renameGroupConversation).not.toHaveBeenCalled();
  });

  it("waits for Chinese composition to finish before Enter saves", async () => {
    render(<ConversationTitleEditor conversationId="chat" onClose={vi.fn()} />);
    const input = await screen.findByDisplayValue("Original");
    fireEvent.change(input, { target: { value: "中文名称" } });
    fireEvent.keyDown(input, { key: "Enter", keyCode: 13, isComposing: true });
    expect(renameGroupConversation).not.toHaveBeenCalled();
    fireEvent.keyUp(input, { key: "Enter", keyCode: 13 });
    fireEvent.keyDown(input, { key: "Enter", keyCode: 13, isComposing: false });
    await waitFor(() => expect(renameGroupConversation).toHaveBeenCalledWith("chat", "中文名称", 4));
  });

  it("keeps a draft when focus leaves the input and cancels without saving", async () => {
    const close = vi.fn();
    render(<ConversationTitleEditor conversationId="chat" onClose={close} />);
    const input = await screen.findByDisplayValue("Original");
    fireEvent.change(input, { target: { value: "  Custom  " } });
    fireEvent.blur(input);
    expect(renameGroupConversation).not.toHaveBeenCalled();
    expect(close).not.toHaveBeenCalled();
    expect(input).toHaveValue("  Custom  ");
    fireEvent.click(screen.getByRole("button", { name: "common.cancel" }));
    expect(close).toHaveBeenCalledOnce();
    expect(renameGroupConversation).not.toHaveBeenCalled();
  });

  it("closes an unchanged name on Save without updating its revision", async () => {
    const close = vi.fn();
    render(<ConversationTitleEditor conversationId="chat" onClose={close} />);
    await screen.findByDisplayValue("Original");
    await waitFor(() => expect(screen.getByRole("button", { name: "common.save" })).toBeEnabled());
    fireEvent.click(screen.getByRole("button", { name: "common.save" }));
    expect(close).toHaveBeenCalledOnce();
    expect(renameGroupConversation).not.toHaveBeenCalled();
  });

  it("allows cancel while loading and ignores the late response", async () => {
    let finish!: (value: unknown) => void;
    mocks.detail.mockReturnValue(new Promise(resolve => { finish = resolve; }));
    const close = vi.fn();
    const view = render(<ConversationTitleEditor conversationId="chat" initialTitle="Original" onClose={close} />);
    const input = screen.getByRole("textbox");
    expect(input).toHaveAttribute("readonly");
    expect(screen.getByRole("button", { name: "common.save" })).toBeDisabled();
    fireEvent.blur(input);
    expect(close).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "common.cancel" }));
    expect(close).toHaveBeenCalledOnce();
    view.unmount();
    await act(async () => finish({ data: { conversation: { display_name: "Late", title_revision: 5 } } }));
    expect(renameGroupConversation).not.toHaveBeenCalled();
  });

  it("retains a failed draft and retries only after an explicit Save", async () => {
    const close = vi.fn();
    vi.mocked(renameGroupConversation).mockRejectedValueOnce(new Error("private failure"));
    render(<ConversationTitleEditor conversationId="chat" onClose={close} />);
    const input = await screen.findByDisplayValue("Original");
    fireEvent.change(input, { target: { value: "Custom" } });
    fireEvent.click(screen.getByRole("button", { name: "common.save" }));
    await screen.findByText("conversationRename.saveFailed");
    expect(input).toHaveValue("Custom");
    fireEvent.blur(input);
    expect(renameGroupConversation).toHaveBeenCalledTimes(1);
    expect(close).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "common.save" }));
    await waitFor(() => expect(close).toHaveBeenCalledOnce());
  });

  it("ignores an obsolete detail response after the loading effect restarts", async () => {
    let resolve!: (value: unknown) => void;
    mocks.detail.mockReturnValueOnce(new Promise(done => { resolve = done; }))
      .mockResolvedValueOnce({ data: { conversation: { display_name: "Latest", title_revision: 5 } } });
    render(<StrictMode><ConversationTitleEditor conversationId="chat" onClose={vi.fn()} /></StrictMode>);
    await screen.findByDisplayValue("Latest");
    await act(async () => resolve({ data: { conversation: { display_name: "Obsolete", title_revision: 4 } } }));
    expect(screen.getByRole("textbox")).toHaveValue("Latest");
  });
  it("cancels without saving and restores focus to the trigger", async () => {
    const trigger = document.createElement("button");
    document.body.appendChild(trigger);
    trigger.focus();
    const close = vi.fn();
    const view = render(<ConversationTitleEditor conversationId="chat" onClose={close} />);
    await screen.findByDisplayValue("Original");
    fireEvent.keyDown(screen.getByRole("textbox"), { key: "Escape" });
    expect(close).toHaveBeenCalledOnce();
    expect(renameGroupConversation).not.toHaveBeenCalled();
    view.unmount();
    await waitFor(() => expect(trigger).toHaveFocus());
    trigger.remove();
  });
  it("loads the latest revision, trims the title, and submits once while pending", async () => {
    let finish!: (value: { display_name: string; title_revision: number }) => void;
    vi.mocked(renameGroupConversation).mockReturnValue(new Promise(resolve => { finish = resolve; }));
    const close = vi.fn();
    const renamed = vi.fn();
    window.addEventListener("lazymind:conversation-title-changed", renamed);
    render(<ConversationTitleEditor conversationId="work" onClose={close} />);
    const input = await screen.findByDisplayValue("Original");
    fireEvent.change(input, { target: { value: "  Custom  " } });
    fireEvent.keyDown(screen.getByRole("textbox"), { key: "Enter" });
    fireEvent.click(screen.getByRole("button", { name: "common.save" }));
    fireEvent.blur(input);
    fireEvent.keyDown(input, { key: "Enter", code: "Enter" });
    expect(screen.getByRole("button", { name: "common.cancel" })).toBeDisabled();
    expect(renameGroupConversation).toHaveBeenCalledOnce();
    expect(renameGroupConversation).toHaveBeenCalledWith("work", "Custom", 4);
    expect(close).not.toHaveBeenCalled();
    await act(async () => finish({ display_name: "Custom", title_revision: 5 }));
    expect(close).toHaveBeenCalledOnce();
    expect(emitConversationGroupsChanged).toHaveBeenCalledOnce();
    expect(renamed.mock.calls[0][0].detail).toEqual({ conversationId: "work", displayName: "Custom", titleRevision: 5 });
    window.removeEventListener("lazymind:conversation-title-changed", renamed);
  });

  it("rejects blank and oversized names and permits 255 Unicode characters", async () => {
    render(<ConversationTitleEditor conversationId="chat" onClose={vi.fn()} />);
    const input = await screen.findByDisplayValue("Original");
    for (const value of ["   ", "字".repeat(256)]) {
      fireEvent.change(input, { target: { value } });
      expect(input).toHaveAttribute("aria-invalid", "true");
      expect(screen.getByRole("button", { name: "common.save" })).toBeDisabled();
      fireEvent.blur(input);
      fireEvent.keyDown(input, { key: "Enter", code: "Enter" });
    }
    expect(renameGroupConversation).not.toHaveBeenCalled();
    fireEvent.change(input, { target: { value: "😀".repeat(255) } });
    expect(input).toHaveAttribute("aria-invalid", "false");
    fireEvent.keyDown(input, { key: "Enter" });
    await waitFor(() => expect(renameGroupConversation).toHaveBeenCalledWith("chat", "😀".repeat(255), 4));
  });

  it("keeps the draft after a conflict and requires another save with the refreshed revision", async () => {
    const close = vi.fn();
    vi.mocked(renameGroupConversation).mockRejectedValueOnce({ response: { status: 409 } });
    render(<ConversationTitleEditor conversationId="chat" onClose={close} />);
    const input = await screen.findByDisplayValue("Original");
    mocks.detail.mockResolvedValue({ data: { conversation: { display_name: "Concurrent title", title_revision: 6 } } });
    fireEvent.change(input, { target: { value: "Custom" } });
    fireEvent.keyDown(screen.getByRole("textbox"), { key: "Enter" });
    await screen.findByText("conversationRename.conflict");
    await waitFor(() => expect(input).not.toHaveAttribute("readonly"));
    fireEvent.blur(input);
    expect(input).toHaveValue("Custom");
    expect(close).not.toHaveBeenCalled();
    expect(renameGroupConversation).toHaveBeenCalledTimes(1);
    fireEvent.keyDown(screen.getByRole("textbox"), { key: "Enter" });
    await waitFor(() => expect(renameGroupConversation).toHaveBeenLastCalledWith("chat", "Custom", 6));
  });

  it("shows a retry after loading fails and preserves input after a save failure", async () => {
    mocks.detail.mockRejectedValueOnce(new Error("private server failure"));
    vi.mocked(renameGroupConversation).mockRejectedValueOnce(new Error("private server failure"));
    render(<ConversationTitleEditor conversationId="chat" onClose={vi.fn()} />);
    await screen.findByText("conversationRename.loadFailed");
    expect(renameGroupConversation).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "common.retry" }));
    const input = await screen.findByDisplayValue("Original");
    fireEvent.change(input, { target: { value: "Draft" } });
    fireEvent.keyDown(screen.getByRole("textbox"), { key: "Enter" });
    await screen.findByText("conversationRename.saveFailed");
    expect(input).toHaveValue("Draft");
    expect(screen.queryByText("private server failure")).not.toBeInTheDocument();
    expect(emitConversationGroupsChanged).not.toHaveBeenCalled();
  });
});
