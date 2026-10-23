import { createRef } from "react";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { ConfigProvider } from "antd";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Dataset } from "@/api/generated/knowledge-client";
import { markPaperLibrary } from "@/modules/knowledge/paper/store";
import UpdateModal, { type UpdateImperativeProps } from "./index";

vi.mock("react-i18next", () => ({ useTranslation: () => ({ t: (key: string) => key }) }));
vi.mock("@/modules/knowledge/utils/request", () => ({
  KnowledgeBaseServiceApi: () => ({
    datasetServiceAllDatasetTags: vi.fn().mockResolvedValue({ data: { tags: ["test-tag"] } }),
    datasetServiceListAlgos: vi.fn().mockResolvedValue({ data: { algos: [{ algo_id: "test-algo" }] } }),
  }),
}));
vi.mock("@/modules/learning/api", () => ({
  getLearningCatalog: vi.fn().mockResolvedValue({ capabilities: [] }),
  getKnowledgeBaseCapabilities: vi.fn().mockResolvedValue([]),
  saveKnowledgeBaseCapabilities: vi.fn().mockResolvedValue({}),
}));

beforeEach(() => localStorage.clear());
afterEach(cleanup);

describe("library type preservation when editing", () => {
  it.each([
    { label: "an explicitly marked legacy paper library", kind: undefined, marked: true, expectedKind: "paper" },
    { label: "an unmarked legacy library even when its name says paper", kind: undefined, marked: false, expectedKind: undefined },
    { label: "a server document library with a stale paper marker", kind: "document", marked: true, expectedKind: undefined },
    { label: "an existing server paper library", kind: "paper", marked: false, expectedKind: undefined },
  ] as const)("submits only the permitted type backfill for $label", async ({ kind, marked, expectedKind }) => {
    const ref = createRef<UpdateImperativeProps>();
    const onUpdate = vi.fn().mockResolvedValue(undefined);
    if (marked) markPaperLibrary("dataset");
    render(<ConfigProvider theme={{ token: { motion: false } }}>
      <UpdateModal ref={ref} onUpdate={onUpdate} />
    </ConfigProvider>);
    const dataset: Dataset = {
      dataset_id: "dataset", display_name: "论文", library_kind: kind,
      type: "DATASET_TYPE_TEXT",
      tags: ["test-tag"], algo: { algo_id: "test-algo" },
    };
    await act(async () => ref.current!.onOpen(dataset));
    fireEvent.click(await screen.findByRole("button", { name: "OK" }));
    await waitFor(() => expect(onUpdate).toHaveBeenCalledOnce());
    if (expectedKind) {
      expect(onUpdate).toHaveBeenCalledWith(expect.objectContaining({ dataset_id: "dataset", library_kind: expectedKind }));
    } else {
      expect(onUpdate.mock.calls[0][0]).not.toHaveProperty("library_kind");
    }
  });
});
