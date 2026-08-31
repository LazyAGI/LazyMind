import { fireEvent, render, screen, within } from "@testing-library/react";
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
vi.mock("./classification", () => ({
  showcaseEntryType: (type: string) => type === "chat" ? "chat" : "work",
  showcaseTechnologyType: (type: string) => type === "workflow" ? "workflow" : "skill",
}));
vi.mock("./CaseCard", () => ({
  default: ({
    item,
    primaryAction,
  }: {
    item: { title: string };
    primaryAction?: string;
  }) => <div data-primary-action={primaryAction}>{item.title}</div>,
}));

const listShowcaseCasesMock = vi.mocked(listShowcaseCases);

describe("GalleryPage", () => {
  beforeEach(() => {
    listShowcaseCasesMock.mockResolvedValue({
      cases: [
        { id: "chat-skill", title: "Chat skill", type: "chat", category: "内容创作", gallery: true, tasks: [] },
        { id: "work-skill", title: "Work skill", type: "work", category: "信息分析", gallery: true, tasks: [] },
        { id: "workflow", title: "Workflow", type: "workflow", category: "内容创作", gallery: true, tasks: [] },
      ],
      categories: ["全部", "内容创作", "信息分析"],
      total: 3,
    } as never);
  });

  it("opens one unified capability center from legacy typed URLs", async () => {
    render(
      <MemoryRouter initialEntries={["/agent/chat/cases?type=work"]}>
        <GalleryPage />
      </MemoryRouter>,
    );

    expect(await screen.findByText("Work skill")).toBeInTheDocument();
    expect(screen.getByText("Work skill")).toHaveAttribute("data-primary-action", "details");
    expect(screen.getByText("Workflow")).toBeInTheDocument();
    expect(screen.getByText("Chat skill")).toBeInTheDocument();
  });

  it("combines task, capability, and technology filters", async () => {
    render(<MemoryRouter><GalleryPage /></MemoryRouter>);
    expect(await screen.findByText("Chat skill")).toBeInTheDocument();

    fireEvent.mouseDown(screen.getByRole("combobox", { name: "showcase.filters.capabilityType" }));
    fireEvent.click(await screen.findByText("showcase.filters.capability.work"));
    expect(screen.queryByText("Chat skill")).not.toBeInTheDocument();
    expect(screen.getByText("Work skill")).toBeInTheDocument();
    expect(screen.getByText("Workflow")).toBeInTheDocument();

    fireEvent.mouseDown(screen.getByRole("combobox", { name: "showcase.filters.technologyType" }));
    fireEvent.click(await screen.findByText("showcase.filters.technology.workflow"));
    expect(screen.queryByText("Work skill")).not.toBeInTheDocument();
    expect(screen.getByText("Workflow")).toBeInTheDocument();

    const taskFilters = screen.getByRole("group", { name: "showcase.filters.taskType" });
    fireEvent.click(within(taskFilters).getByRole("button", { name: "信息分析" }));
    expect(screen.queryByText("Workflow")).not.toBeInTheDocument();
    expect(screen.getByText("showcase.noMatches")).toBeInTheDocument();
  });
});
