import { render, screen } from "@testing-library/react";
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
  matchesShowcaseEntryType: (capabilityType: string, entryType: string) =>
    entryType === "chat"
      ? capabilityType === "chat"
      : capabilityType === "work" || capabilityType === "workflow",
}));
vi.mock("./CaseCard", () => ({ default: ({ item }: { item: { title: string } }) => <div>{item.title}</div> }));

const listShowcaseCasesMock = vi.mocked(listShowcaseCases);

describe("GalleryPage", () => {
  beforeEach(() => {
    listShowcaseCasesMock.mockResolvedValue({
      cases: [
        { id: "chat-skill", title: "Chat skill", type: "chat", gallery: true, tasks: [] },
        { id: "work-skill", title: "Work skill", type: "work", gallery: true, tasks: [] },
        { id: "workflow", title: "Workflow", type: "workflow", gallery: true, tasks: [] },
      ],
      categories: ["全部"],
      total: 2,
    } as never);
  });

  it("keeps the entry-point type when opening the capability center", async () => {
    render(
      <MemoryRouter initialEntries={["/agent/chat/cases?type=work"]}>
        <GalleryPage />
      </MemoryRouter>,
    );

    expect(await screen.findByText("Work skill")).toBeInTheDocument();
    expect(screen.getByText("Workflow")).toBeInTheDocument();
    expect(screen.queryByText("Chat skill")).not.toBeInTheDocument();
  });
});
