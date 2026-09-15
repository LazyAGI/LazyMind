import { act, render, screen } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";
import ConversationRunningIndicator from "./ConversationRunningIndicator";
import { useConversationRunningStore as store } from "@/modules/chat/store/conversationRunning";

vi.mock("@/components/request", () => ({ axiosInstance: {}, BASE_URL: "" }));
vi.mock("react-i18next", () => ({ useTranslation: () => ({ t: (key: string) => key }) }));
beforeEach(() => store.setState({ entries: {}, watchers: {} }));

it("shows independent accessible indicators and removes only the completed conversation", () => {
  store.setState({ entries: { a: { status: "running", confirmedAt: Date.now() }, b: { status: "running", confirmedAt: Date.now() } } });
  render(<><ConversationRunningIndicator conversationId="a" /><ConversationRunningIndicator conversationId="b" /></>);
  expect(screen.getAllByRole("img", { name: "chat.conversationRunning" })).toHaveLength(2);
  act(() => store.setState({ entries: { a: { status: "idle", confirmedAt: Date.now() }, b: { status: "unknown", confirmedAt: 0 } } }));
  expect(screen.queryByRole("img", { name: "chat.conversationRunning" })).not.toBeInTheDocument();
  expect(screen.getByRole("img", { name: "chat.conversationStatusUnavailable" })).toHaveAttribute("tabindex", "0");
});

it.each([
  ['completed', 'chat.conversationCompleted'],
  ['failed', 'chat.conversationFailed'],
  ['canceled', 'chat.conversationCanceled'],
] as const)('keeps an accessible %s result after activity becomes idle', (terminalStatus, label) => {
  store.setState({ entries: { a: { status: 'idle', terminalStatus, confirmedAt: Date.now() } } });
  render(<ConversationRunningIndicator conversationId='a' />);
  expect(screen.getByRole('img', {name:label})).toBeVisible();
});

it("hides viewed results while retaining active and unavailable indicators", () => {
  store.setState({ entries: {
    a: { status: "idle", terminalStatus: "completed", terminalRead: true, confirmedAt: Date.now() },
    b: { status: "running", terminalRead: true, confirmedAt: Date.now() },
    c: { status: "unknown", terminalRead: true, confirmedAt: Date.now() },
  } });
  render(<><ConversationRunningIndicator conversationId="a" /><ConversationRunningIndicator conversationId="b" /><ConversationRunningIndicator conversationId="c" /></>);
  expect(screen.queryByRole("img", { name: "chat.conversationCompleted" })).not.toBeInTheDocument();
  expect(screen.getByRole("img", { name: "chat.conversationRunning" })).toBeVisible();
  expect(screen.getByRole("img", { name: "chat.conversationStatusUnavailable" })).toBeVisible();
});
