import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { expect, it, vi } from "vitest";
import { useTaskCenterStore } from "@/modules/chat/store/taskCenter";
import MailDraftCard from "./index";
vi.mock("react-i18next", async (importOriginal) => ({
  ...await importOriginal<typeof import("react-i18next")>(),
  useTranslation: () => ({ t: (key: string) => key }),
}));
it("renders a draft before conversation artifacts have loaded", () => {
  useTaskCenterStore.setState({ activeConversationId: "mail-no-artifacts", artifactsByConversation: {} });
  render(<MemoryRouter><MailDraftCard draft={{ draft_id: "draft", to: ["team@example.com"] }} onConfirm={vi.fn()} /></MemoryRouter>);
  expect(screen.getByRole("button", { name: "chat.mailDraft.confirmSend" })).toBeEnabled();
});
