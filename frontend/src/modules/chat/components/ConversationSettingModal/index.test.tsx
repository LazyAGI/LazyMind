import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import ConversationSettingModal from "./index";
import { useConversationSettings } from "@/modules/chat/store/conversationSettings";
import { useChatNewMessageStore } from "@/modules/chat/store/chatNewMessage";

const { mockSetStatus, mockMessageSuccess } = vi.hoisted(() => ({
  mockSetStatus: vi.fn(),
  mockMessageSuccess: vi.fn(),
}));

vi.mock("react-i18next", () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}));

// @/components/ui is a barrel that also re-exports RenderPdf, which pulls in
// react-pdf/pdfjs-dist and needs browser canvas APIs jsdom lacks. Mock the
// barrel down to the CommonModal implementation this component actually uses.
vi.mock("@/components/ui", () => ({
  CommonModal: ({
    contentText,
    title,
    successFn,
    cancelFn,
  }: {
    contentText: React.ReactNode;
    title: React.ReactNode;
    successFn?: () => void;
    cancelFn?: () => void;
  }) => (
    <div>
      <div>{title}</div>
      <div>{contentText}</div>
      <button onClick={cancelFn}>common.cancel</button>
      <button onClick={successFn}>common.confirm</button>
    </div>
  ),
}));

vi.mock("@/modules/chat/utils/request", () => ({
  ChatServiceApi: () => ({
    conversationServiceSetMultiAnswersSwitchStatus: mockSetStatus,
  }),
}));

vi.mock("@/components/request", () => ({
  localizeErrorCode: (code: string) => `errors.${code}`,
}));

vi.mock("antd", async () => {
  const actual = await vi.importActual<typeof import("antd")>("antd");
  return {
    ...actual,
    message: { success: mockMessageSuccess, error: vi.fn() },
  };
});

describe("ConversationSettingModal", () => {
  beforeEach(() => {
    mockSetStatus.mockReset();
    mockMessageSuccess.mockReset();
    useConversationSettings.setState({ enableMultipleAnswers: false, isLoading: false });
    useChatNewMessageStore.setState({ newMessage: true });
  });

  it("initializes the local switch based on initialStatus", () => {
    render(<ConversationSettingModal cancelFn={vi.fn()} initialStatus={1} />);
    const switchInput = screen.getByRole("switch");
    expect(switchInput).toHaveAttribute("aria-checked", "true");
  });

  it("saves the toggled preference and shows the settingsSaved message", async () => {
    mockSetStatus.mockResolvedValueOnce({ data: { status: 1 } });
    const cancelFn = vi.fn();
    const onStatusChange = vi.fn();
    render(
      <ConversationSettingModal
        cancelFn={cancelFn}
        initialStatus={0}
        onStatusChange={onStatusChange}
      />,
    );

    fireEvent.click(screen.getByRole("switch"));
    fireEvent.click(screen.getByText("common.confirm"));

    await waitFor(() => expect(mockSetStatus).toHaveBeenCalledWith({
      setMultiAnswersSwitchStatusRequest: { status: 1 },
    }));
    expect(mockMessageSuccess).toHaveBeenCalledWith("chat.settingsSaved");
    expect(onStatusChange).toHaveBeenCalled();
    expect(cancelFn).toHaveBeenCalled();
  });

  it("shows the keepLazyMindAnswer message when disabling while there is no new message", async () => {
    mockSetStatus.mockResolvedValueOnce({ data: { status: 0 } });
    useChatNewMessageStore.setState({ newMessage: false });

    render(<ConversationSettingModal cancelFn={vi.fn()} initialStatus={1} />);
    fireEvent.click(screen.getByRole("switch"));
    fireEvent.click(screen.getByText("common.confirm"));

    await waitFor(() =>
      expect(mockMessageSuccess).toHaveBeenCalledWith("chat.keepLazyMindAnswer"),
    );
  });

  it("swallows client-side errors without showing a toast when the request never went out", async () => {
    mockSetStatus.mockRejectedValueOnce(new Error("client error"));
    const cancelFn = vi.fn();
    render(<ConversationSettingModal cancelFn={cancelFn} initialStatus={0} />);

    fireEvent.click(screen.getByText("common.confirm"));

    await waitFor(() => expect(mockSetStatus).toHaveBeenCalled());
    expect(cancelFn).not.toHaveBeenCalled();
  });
});
