import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import type { ConversationFollowUp } from "../../utils/request";
import FollowUpQueue from "./FollowUpQueue";

vi.mock("react-i18next", async (importOriginal) => ({
  ...(await importOriginal<typeof import("react-i18next")>()),
  useTranslation: () => ({ t: (key: string) => key }),
}));

function item(overrides: Partial<ConversationFollowUp> = {}): ConversationFollowUp {
  return {
    id: "follow-up-1",
    conversation_id: "conversation-1",
    content: { text: "Keep the scope small" },
    text: "Keep the scope small",
    mode: "queued",
    status: "pending",
    sort_order: 1024,
    revision: 1,
    created_at: "2026-10-21T00:00:00Z",
    updated_at: "2026-10-21T00:00:00Z",
    ...overrides,
  };
}

function renderQueue(items: ConversationFollowUp[], isStreaming = true) {
  const handlers = {
    onSteer: vi.fn(),
    onRetry: vi.fn(),
    onRemove: vi.fn(),
    onUpdate: vi.fn(),
    onReorder: vi.fn(),
  };
  render(<FollowUpQueue items={items} isStreaming={isStreaming} {...handlers} />);
  return handlers;
}

describe("FollowUpQueue", () => {
  it("allows a pending message to steer the active task", () => {
    const pending = item();
    const handlers = renderQueue([pending]);
    fireEvent.click(screen.getByRole("button", { name: "chat.followUpSteer" }));
    expect(handlers.onSteer).toHaveBeenCalledWith(pending);
    expect(screen.getByText("chat.followUpPending")).toBeInTheDocument();
  });

  it("uses the conversation message as implicit acknowledgement and hides accepted guidance", () => {
    renderQueue([item({ status: "accepted", mode: "steer", accepted_at: new Date().toISOString() })]);
    expect(screen.queryByText("Keep the scope small")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "chat.followUpSteer" })).not.toBeInTheDocument();
  });

  it("does not restore an old accepted card from polling", () => {
    renderQueue([item({
      status: "accepted",
      mode: "steer",
      accepted_at: "2026-10-20T00:00:00Z",
    })]);
    expect(screen.queryByText("Keep the scope small")).not.toBeInTheDocument();
  });

  it("keeps failed text and exposes retry", () => {
    const failed = item({ status: "failed", mode: "steer", error_message: "offline" });
    const handlers = renderQueue([failed]);
    expect(screen.getByText(/offline/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "chat.followUpRetry" }));
    expect(handlers.onRetry).toHaveBeenCalledWith(failed);
  });
});
