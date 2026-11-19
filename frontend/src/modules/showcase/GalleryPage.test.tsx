import { fireEvent, render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import GalleryPage from "./GalleryPage";
import { listShowcaseCases } from "./api";

vi.mock("react-i18next", () => ({
  useTranslation: () => ({
    i18n: { language: "zh-CN", resolvedLanguage: "zh-CN" },
    t: (key: string) => key,
  }),
}));

vi.mock("./api", () => ({
  listShowcaseCases: vi.fn(),
}));
vi.mock("./CaseCard", () => ({
  default: ({
    item,
    primaryAction,
    entryType,
  }: {
    item: { title: string };
    primaryAction?: string;
    entryType?: string;
  }) => <div data-primary-action={primaryAction} data-entry-type={entryType}>{item.title}</div>,
}));

const listShowcaseCasesMock = vi.mocked(listShowcaseCases);

describe("GalleryPage", () => {
  beforeEach(() => {
    listShowcaseCasesMock.mockResolvedValue({
      cases: [
        { id: "chat-skill", title: "Chat skill", type: "chat", category: "内容创作", gallery: true, tasks: [] },
        { id: "work-skill", title: "Work skill", type: "work", category: "信息分析", gallery: true, tasks: [] },
        { id: "workflow", title: "Workflow", type: "workflow", category: "内容创作", gallery: true, tasks: [] },
        { id: "paper-reading", title: "论文阅读", type: "page", category: "信息分析", gallery: true, tasks: [], featured_order: 0 },
      ],
      categories: ["全部", "内容创作", "信息分析"],
      total: 3,
    } as never);
  });

  it.each(["chat", "work"] as const)("keeps %s capabilities separate in the full gallery", async (mode) => {
    render(
      <MemoryRouter initialEntries={[`/agent/chat/cases?showcase_entry=${mode}`]}>
        <GalleryPage />
      </MemoryRouter>,
    );

    expect(await screen.findByText("论文阅读")).toBeInTheDocument();
    for (const title of ["Chat skill", "Work skill", "Workflow"]) {
      const card = screen.queryByText(title);
      expect(Boolean(card)).toBe(title === "Chat skill" ? mode === "chat" : mode === "work");
      if (card) expect(card).toHaveAttribute("data-entry-type", mode);
    }
    expect(screen.queryByRole("combobox", { name: "showcase.filters.capabilityType" })).not.toBeInTheDocument();
  });

  it("keeps featured Skills as chat cases and chat enhancements", async () => {
    listShowcaseCasesMock.mockResolvedValueOnce({
      cases: [
        { id: "mbti-money-personality-test", title: "MBTI", type: "chat", category: "金融与投资分析", gallery: true, tasks: [] },
        { id: "prompt-engineering-expert", title: "Prompt Engineering", type: "chat", category: "提示词与能力构建", gallery: true, tasks: [] },
        { id: "graphic-design", title: "Graphic Design", type: "chat", category: "多媒体设计与创意", gallery: true, tasks: [] },
        { id: "live-commerce-script-studio", title: "Live Commerce", type: "chat", category: "内容营销与社媒", gallery: true, tasks: [] },
        { id: "skill-creator", title: "Skill Creator", type: "chat", category: "提示词与能力构建", gallery: true, tasks: [] },
      ],
      categories: ["全部", "金融与投资分析", "提示词与能力构建", "多媒体设计与创意", "内容营销与社媒"],
      total: 5,
    } as never);

    render(<MemoryRouter initialEntries={["/agent/chat/cases?showcase_entry=chat"]}><GalleryPage /></MemoryRouter>);
    expect(await screen.findByText("MBTI")).toBeInTheDocument();

    ["MBTI", "Prompt Engineering", "Graphic Design", "Live Commerce", "Skill Creator"].forEach((title) => {
      expect(screen.getByText(title)).toBeInTheDocument();
    });

    fireEvent.mouseDown(screen.getByRole("combobox", { name: "showcase.filters.technologyType" }));
    fireEvent.click(await screen.findByText("showcase.filters.technology.skill"));
    ["MBTI", "Prompt Engineering", "Graphic Design", "Live Commerce", "Skill Creator"].forEach((title) => {
      expect(screen.getByText(title)).toBeInTheDocument();
    });

    fireEvent.mouseDown(screen.getByRole("combobox", { name: "showcase.filters.technologyType" }));
    fireEvent.click(await screen.findByText("showcase.filters.technology.workflow"));
    expect(screen.getByText("showcase.noMatches")).toBeInTheDocument();
  });

  it.each(["chat", "work"] as const)("finds paper reading from the %s entry and the page form", async (mode) => {
    render(<MemoryRouter initialEntries={[`/agent/chat/cases?showcase_entry=${mode}`]}><GalleryPage /></MemoryRouter>);
    await screen.findByText("论文阅读");
    expect(screen.getByText("论文阅读")).toHaveAttribute("data-entry-type", mode);
    fireEvent.change(screen.getByRole("textbox", { name: "showcase.searchLabel" }), { target: { value: "论文阅读" } });
    expect(screen.getByText("论文阅读")).toBeInTheDocument();
    expect(screen.queryByText("Workflow")).not.toBeInTheDocument();
    fireEvent.mouseDown(screen.getByRole("combobox", { name: "showcase.filters.technologyType" }));
    fireEvent.click(await screen.findByText("showcase.filters.technology.page"));
    expect(screen.getByText("论文阅读")).toBeInTheDocument();
  });
});
