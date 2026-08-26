import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import FeaturedCases from "./FeaturedCases";
import { listShowcaseCases } from "./api";

vi.mock("react-i18next", () => ({
  useTranslation: () => ({
    i18n: { language: "zh-CN", resolvedLanguage: "zh-CN" },
    t: (key: string) => ({
      "showcase.featuredTitle": "精选能力",
      "showcase.viewMore": "查看更多",
    })[key] || key,
  }),
}));

vi.mock("./api", () => ({
  listShowcaseCases: vi.fn(),
  matchesShowcaseEntryType: (capabilityType: string, entryType: string) =>
    entryType === "chat"
      ? capabilityType === "chat"
      : capabilityType === "work" || capabilityType === "workflow",
}));
vi.mock("./CaseCard", () => ({ default: ({ item }: { item: { title: string } }) => <div>{item.title}</div> }));

const listShowcaseCasesMock = vi.mocked(listShowcaseCases);

describe("FeaturedCases", () => {
  beforeEach(() => {
    listShowcaseCasesMock.mockResolvedValue({
      cases: [
        { id: "chat-skill", title: "Chat skill", type: "chat", featured: true },
        { id: "work-skill", title: "Work skill", type: "work", featured: true },
        { id: "workflow", title: "Workflow", type: "workflow", featured: true },
      ],
      categories: [],
      total: 2,
    } as never);
  });

  it("renders only the Featured Skill type selected by the entry point", async () => {
    const view = render(<MemoryRouter><FeaturedCases type="chat" /></MemoryRouter>);

    expect(await screen.findByText("Chat skill")).toBeInTheDocument();
    expect(screen.queryByText("Work skill")).not.toBeInTheDocument();
    expect(screen.queryByRole("radio")).not.toBeInTheDocument();
    expect(screen.getByRole("link", { name: /查看更多/ })).toHaveAttribute(
      "href",
      "/agent/chat/cases?type=chat",
    );

    view.rerender(<MemoryRouter><FeaturedCases type="work" /></MemoryRouter>);

    expect(await screen.findByText("Work skill")).toBeInTheDocument();
    expect(screen.getByText("Workflow")).toBeInTheDocument();
    expect(screen.queryByText("Chat skill")).not.toBeInTheDocument();
  });
});
