import { act, fireEvent, renderHook, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { Modal, message, type ModalFuncProps } from "antd";
import { useWorkflowSelection } from "./useWorkflowSelection";
import { setupAntdStaticCleanup } from "@/test/antdStatic";

const mocks = vi.hoisted(() => ({ getDetail: vi.fn(), patch: vi.fn(), getDefaults: vi.fn() }));
vi.mock("react-i18next", async (original) => ({
  ...await original<typeof import("react-i18next")>(),
  useTranslation: () => ({ t: (key: string) => key }),
}));
vi.mock("../../utils/request", async (original) => ({
  ...await original<typeof import("../../utils/request")>(),
  ChatServiceApi: () => ({ conversationServiceGetConversationDetail: mocks.getDetail }),
  ConversationSettingsApi: () => ({ patchConversationSettings: mocks.patch, getChatSettings: mocks.getDefaults }),
}));

describe("workflow selection consent", () => {
  setupAntdStaticCleanup();
  let confirmation: ModalFuncProps;
  let destroy: ReturnType<typeof vi.fn>;
  beforeEach(() => {
    vi.restoreAllMocks();
    vi.clearAllMocks();
    destroy = vi.fn();
    vi.spyOn(Modal, "confirm").mockImplementation((props: ModalFuncProps) => {
      confirmation = props;
      return { destroy, update: vi.fn() };
    });
    mocks.getDetail.mockResolvedValue({ data: { conversation: { enable_workflow: false, workflow_mode: "auto", enable_subagent: true } } });
    mocks.patch.mockResolvedValue({});
  });

  it("keeps workflows disabled when the user cancels", async () => {
    const onChange = vi.fn();
    const { result } = renderHook(() => useWorkflowSelection({ conversationId: "chat-1", runInBackground: false, onChange }));
    let selected!: Promise<boolean>;
    act(() => { selected = result.current.beforeWorkflowSelect(); });
    await waitFor(() => expect(Modal.confirm).toHaveBeenCalled());
    expect(confirmation).toMatchObject({ okText: "chat.workflowSelectionEnable", cancelText: "chat.workflowSelectionKeepDisabled" });
    await act(async () => { confirmation.onCancel?.(); expect(await selected).toBe(false); });
    expect(mocks.patch).not.toHaveBeenCalled();
    expect(onChange).not.toHaveBeenCalled();
  });

  it("only selects after the confirmed settings patch succeeds", async () => {
    let completeSave!: () => void;
    mocks.patch.mockImplementation(() => new Promise<void>(resolve => { completeSave = resolve; }));
    const onChange = vi.fn();
    const { result } = renderHook(() => useWorkflowSelection({ conversationId: "chat-1", runInBackground: false, onChange }));
    let selected!: Promise<boolean>;
    act(() => { selected = result.current.beforeWorkflowSelect(); });
    await waitFor(() => expect(Modal.confirm).toHaveBeenCalled());
    expect(mocks.patch).not.toHaveBeenCalled();
    let saving!: Promise<void>;
    act(() => { saving = confirmation.onOk?.(); });
    expect(mocks.patch).toHaveBeenCalledWith("chat-1", { enable_workflow: true, workflow_mode: "dynamic" });
    expect(onChange).not.toHaveBeenCalled();
    expect(result.current.busy).toBe(true);
    await act(async () => { completeSave(); await saving; expect(await selected).toBe(true); });
    expect(onChange).toHaveBeenCalledWith({ enable_workflow: true, workflow_mode: "dynamic", enable_subagent: true });
  });

  it("reports save failure without granting permission", async () => {
    const error = vi.spyOn(message, "error").mockImplementation(() => (() => undefined) as never);
    mocks.patch.mockRejectedValue(new Error("save failed"));
    const onChange = vi.fn();
    const { result } = renderHook(() => useWorkflowSelection({ conversationId: "chat-1", runInBackground: false, onChange }));
    let selected!: Promise<boolean>;
    act(() => { selected = result.current.beforeWorkflowSelect(); });
    await waitFor(() => expect(Modal.confirm).toHaveBeenCalled());
    await act(async () => { await confirmation.onOk?.(); expect(await selected).toBe(false); });
    expect(error).toHaveBeenCalled();
    expect(onChange).not.toHaveBeenCalled();
  });

  it("enables only the current new-chat draft", async () => {
    vi.restoreAllMocks();
    const onChange = vi.fn();
    const { result } = renderHook(() => useWorkflowSelection({ runInBackground: false, settings: { enable_workflow: false }, onChange }));
    let selected!: Promise<boolean>;
    act(() => { selected = result.current.beforeWorkflowSelect(); });
    fireEvent.click(await screen.findByRole("button", { name: "chat.workflowSelectionEnable" }));
    await act(async () => { expect(await selected).toBe(true); });
    expect(mocks.patch).not.toHaveBeenCalled();
    expect(onChange).toHaveBeenCalledWith({ enable_workflow: true, workflow_mode: "dynamic" });
  });

  it("blocks sending if the server disabled workflows after selection", async () => {
    const warning = vi.spyOn(message, "warning").mockImplementation(() => (() => undefined) as never);
    const onChange = vi.fn();
    const { result } = renderHook(() => useWorkflowSelection({ conversationId: "chat-1", runInBackground: false, settings: { enable_workflow: true }, onChange }));
    expect(await result.current.validateWorkflowSelection()).toBe(false);
    expect(warning).toHaveBeenCalledWith("chat.workflowSelectionDisabledNotice");
    expect(mocks.patch).not.toHaveBeenCalled();
  });

  it("cancels an open confirmation when switching chats", async () => {
    const onChange = vi.fn();
    const { result, rerender } = renderHook(({ conversationId }) => useWorkflowSelection({ conversationId, runInBackground: false, onChange }), { initialProps: { conversationId: "chat-1" } });
    let selected!: Promise<boolean>;
    act(() => { selected = result.current.beforeWorkflowSelect(); });
    await waitFor(() => expect(Modal.confirm).toHaveBeenCalled());
    rerender({ conversationId: "chat-2" });
    expect(await selected).toBe(false);
    expect(destroy).toHaveBeenCalled();
    expect(onChange).not.toHaveBeenCalled();
    expect(mocks.patch).not.toHaveBeenCalled();
  });
});
