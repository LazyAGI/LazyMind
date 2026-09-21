import { fireEvent, render, screen } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { ChatComposer, ChatMessageStream } from "./ChatSection";

vi.mock("react-i18next", () => ({ useTranslation: () => ({ t: (key: string) => key }) }));

const props = () => ({
  activeStepText: "dataset", isAutoMode: false, isSendingMessage: false,
  prompt: "调整生成要求", onPromptChange: vi.fn(), onSend: vi.fn(),
  renderKnowledgeAndModeTools: () => null,
  renderSendButton: () => <button>send</button>,
});

it("keeps the manual composer visible and explains why a terminated task cannot send", () => {
  const input = props();
  render(<ChatComposer {...input} readOnlyReason="任务已终止，请新建任务。" />);
  const textbox = screen.getByRole("textbox");
  expect(textbox).toBeDisabled();
  expect(textbox).toHaveAccessibleDescription("任务已终止，请新建任务。");
  fireEvent.keyDown(textbox, { key: "Enter" });
  expect(input.onSend).not.toHaveBeenCalled();
  expect(screen.queryByText("send")).not.toBeInTheDocument();
});

it("sends manual input on Enter but preserves Shift Enter and input composition", () => {
  const input = props();
  render(<ChatComposer {...input} />);
  const textbox = screen.getByRole("textbox");
  expect(textbox).toBeEnabled();
  fireEvent.keyDown(textbox, { key: "Enter", shiftKey: true });
  fireEvent.keyDown(textbox, { key: "Enter", isComposing: true, keyCode: 229 });
  expect(input.onSend).not.toHaveBeenCalled();
  fireEvent.keyDown(textbox, { key: "Enter" });
  expect(input.onSend).toHaveBeenCalledOnce();
});

it("does not tell a read-only conversation to use an unavailable input", () => {
  render(<ChatMessageStream isAutoInteractionActive={false} messages={[]} streamRef={null} readOnlyReason="任务已终止，请新建任务。" />);
  expect(screen.getByText("任务已终止，请新建任务。")).toBeVisible();
  expect(screen.queryByText("selfEvolutionRun.emptyChatPlaceholder")).not.toBeInTheDocument();
});
