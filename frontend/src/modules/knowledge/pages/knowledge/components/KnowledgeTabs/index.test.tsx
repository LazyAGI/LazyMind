import { cleanup, render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Doc } from "@/api/generated/core-client";
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

const document: Doc = {
  convert_file_uri: "",
  create_time: "2026-10-22T00:00:00Z",
  creator: "test-user",
  data_source_type: "local",
  dataset_display: "Sample dataset",
  dataset_id: "ds",
  display_name: "sample.md",
  document_id: "doc",
  document_size: 128,
  document_stage: "ready",
  file_id: "file",
  file_system_path: "",
  name: "sample.md",
  p_id: "",
  rel_path: "sample.md",
  retained_only: false,
  type: "file",
  update_time: "2026-10-22T00:00:00Z",
  uri: "",
};

describe("native document segments", () => {
  it.each(["missing", "checking", "error"])("shows read-only chunks when RAG is %s", (availability) => {
    state.availability = availability;
    render(<MemoryRouter><KnowledgeTabs knowledgeDetail={document} /></MemoryRouter>);
    expect(screen.getByText("block:readonly")).toBeTruthy();
    expect(state.dataset).not.toHaveBeenCalled();
  });
});
