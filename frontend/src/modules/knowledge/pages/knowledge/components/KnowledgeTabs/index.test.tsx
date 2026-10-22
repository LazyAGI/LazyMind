import { cleanup, render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";
import KnowledgeTabs from "./index";

const state = vi.hoisted(() => ({ availability: "missing", dataset: vi.fn() }));
vi.mock("react-i18next", () => ({ useTranslation: () => ({ t: (key: string) => key }) }));
vi.mock("@/modules/modelProvider/contexts/RAGComponentContext", () => ({
  useRAGComponent: () => ({ availability: state.availability }),
}));
vi.mock("@/modules/knowledge/utils/request", () => ({
  KnowledgeBaseServiceApi: () => ({ datasetServiceGetDataset: state.dataset }),
}));
vi.mock("../SegmentTab", () => ({
  default: ({ type, editable }: { type: string; editable: boolean }) =>
    <div>{type}:{editable ? "editable" : "readonly"}</div>,
}));
vi.mock("../SummaryTab", () => ({ default: () => null }));
vi.mock("../QaTab", () => ({ default: () => null }));
afterEach(() => { cleanup(); vi.clearAllMocks(); });

describe("native document segments", () => {
  it.each(["missing", "checking", "error"])("shows read-only chunks when RAG is %s", (availability) => {
    state.availability = availability;
    render(<MemoryRouter><KnowledgeTabs knowledgeDetail={{ display_name: "sample.md", dataset_id: "ds" }} /></MemoryRouter>);
    expect(screen.getByText("block:readonly")).toBeTruthy();
    expect(state.dataset).not.toHaveBeenCalled();
  });
});
