import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";

import ConversationRelationBanner from "./index";

vi.mock("react-i18next", () => ({
  useTranslation: () => ({
    t: (key: string, params?: { parent?: string }) =>
      ({
        "chat.conversationRelationBannerLabel": "子会话来源",
        "chat.conversationSourceFrom": `来源：${params?.parent}`,
        "chat.conversationForkedFrom": `Fork自：${params?.parent}`,
        "chat.returnToParentConversation": "返回主会话",
      })[key] || key,
  }),
}));

describe("ConversationRelationBanner", () => {
  it("links a sidechat back to its parent conversation", () => {
    render(
      <MemoryRouter>
        <ConversationRelationBanner
          relation={{
            parentConversationId: "parent/1",
            parentDisplayName: "产品方案",
            relationType: "sidechat",
          }}
        />
      </MemoryRouter>,
    );

    expect(screen.getByRole("region", { name: "子会话来源" })).toHaveTextContent(
      "来源：产品方案",
    );
    expect(screen.getByRole("link", { name: "返回主会话" })).toHaveAttribute(
      "href",
      "/agent/chat/home/parent%2F1",
    );
  });

  it("uses distinct copy for a fork", () => {
    render(
      <MemoryRouter>
        <ConversationRelationBanner
          relation={{
            parentConversationId: "parent",
            parentDisplayName: "技术讨论",
            relationType: "fork",
          }}
        />
      </MemoryRouter>,
    );

    expect(screen.getByText("Fork自：技术讨论")).toBeInTheDocument();
  });
});

it("locates an exact source reply and disables navigation after source deletion", () => {
  const relation = { parentConversationId: "source", parentDisplayName: "Source", relationType: "fork" as const, sourceHistoryId: "h/40", canLocate: true, sourceStatus: "available" };
  const { rerender } = render(<MemoryRouter><ConversationRelationBanner relation={relation} /></MemoryRouter>);
  expect(screen.getByRole("link", { name: "chat.fork.locateSource" })).toHaveAttribute("href", "/agent/chat/home/source?anchor_history_id=h%2F40");
  rerender(<MemoryRouter><ConversationRelationBanner relation={{ ...relation, canLocate: false, sourceStatus: "deleted" }} /></MemoryRouter>);
  expect(screen.queryByRole("link")).not.toBeInTheDocument();
  expect(screen.getByText("chat.fork.source.deleted")).toBeInTheDocument();
});
