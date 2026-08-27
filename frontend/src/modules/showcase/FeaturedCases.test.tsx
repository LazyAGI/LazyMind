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
        { id: "chat-skill", title: "Chat skill", type: "chat", featured: true, featured_order: 1 },
        { id: "work-skill", title: "Work skill", type: "work", featured: true, featured_order: 2 },
        { id: "workflow", title: "Workflow", type: "workflow", featured: true, featured_order: 1 },
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
      "/agent/chat/cases",
    );

    view.rerender(<MemoryRouter><FeaturedCases type="work" /></MemoryRouter>);

    expect(await screen.findByText("Work skill")).toBeInTheDocument();
    expect(screen.getByText("Workflow")).toBeInTheDocument();
    expect(screen.queryByText("Chat skill")).not.toBeInTheDocument();
  });

  it("limits the home preview and leaves the full list behind View More", async () => {
    listShowcaseCasesMock.mockResolvedValue({
      cases: Array.from({ length: 9 }, (_, index) => ({
        id: `work-${index + 1}`,
        title: `Work ${index + 1}`,
        type: "work",
        featured: true,
        featured_order: index + 1,
      })),
      categories: [],
      total: 9,
    } as never);

    render(<MemoryRouter><FeaturedCases type="work" /></MemoryRouter>);

    expect(await screen.findByText("Work 1")).toBeInTheDocument();
    expect(screen.getByText("Work 8")).toBeInTheDocument();
    expect(screen.queryByText("Work 9")).not.toBeInTheDocument();
    expect(screen.getByRole("link", { name: /查看更多/ })).toHaveAttribute(
      "href",
      "/agent/chat/cases",
    );
  });

  it("sorts each entry point by the resolved Featured order", async () => {
    listShowcaseCasesMock.mockResolvedValue({
      cases: [
        { id: "later", title: "Later", type: "work", featured: true, featured_order: 2 },
        { id: "first", title: "First", type: "workflow", featured: true, featured_order: 1 },
      ],
      categories: [],
      total: 2,
    } as never);

    render(<MemoryRouter><FeaturedCases type="work" /></MemoryRouter>);

    const cards = await screen.findAllByText(/First|Later/);
    expect(cards.map((card) => card.textContent)).toEqual(["First", "Later"]);
  });
});
