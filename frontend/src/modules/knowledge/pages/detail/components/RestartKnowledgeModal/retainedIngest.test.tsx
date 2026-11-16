import { createRef } from "react";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import RestartKnowledgeModal, { type IRestartKnowledgeProps } from "./index";

const mocks = vi.hoisted(() => ({
  wait: vi.fn().mockResolvedValue(undefined),
  create: vi.fn().mockResolvedValue({ data: { tasks: [{ task_id: "task-1" }] } }),
  start: vi.fn().mockResolvedValue({ data: { started_count: 1 } }),
}));
vi.mock("react-i18next", () => ({ useTranslation: () => ({ t: (key: string) => key }) }));
vi.mock("@/modules/modelProvider/contexts/RAGComponentContext", () => ({ useRAGComponent: () => ({ availability: "ready" }) }));
vi.mock("@/runtime/readiness", () => ({ waitForRuntimeCapability: mocks.wait }));
vi.mock("@/components/request", () => ({ localizeErrorCode: (code: string) => code }));
vi.mock("@/modules/knowledge/utils/importError", () => ({ getImportErrorMessage: (error: Error) => error.message }));
vi.mock("@/modules/knowledge/utils/request", () => ({ TaskServiceApi: () => ({ createTasks: mocks.create, startTasks: mocks.start }) }));
afterEach(() => { cleanup(); vi.clearAllMocks(); });

describe("retained file ingestion", () => {
  it("explains the stored processing level and prevents submitting an invalid ingestion", async () => {
    const ref = createRef<IRestartKnowledgeProps>();
    render(<RestartKnowledgeModal ref={ref} onFinish={vi.fn()} />);
    await act(async () => ref.current!.onOpen({ dataset: "library", ids: ["doc"], title: "Ingest", processingLevel: "stored", ingest: true }));
    expect(screen.getByText("knowledge.ingestRetainedRequiresProcessing")).toBeInTheDocument();
    const confirm = screen.getByRole("button", { name: "OK" });
    expect(confirm).toBeDisabled();
    fireEvent.click(confirm);
    expect(mocks.wait).not.toHaveBeenCalled();
    expect(mocks.create).not.toHaveBeenCalled();
    expect(mocks.start).not.toHaveBeenCalled();
  });

  it.each(["parsed", "chunked", "indexed", undefined] as const)("allows retained ingestion at %s level", async processingLevel => {
    const ref = createRef<IRestartKnowledgeProps>();
    const onFinish = vi.fn();
    render(<RestartKnowledgeModal ref={ref} onFinish={onFinish} />);
    await act(async () => ref.current!.onOpen({ dataset: "library", ids: ["doc"], title: "Ingest", processingLevel, ingest: true }));
    expect(screen.queryByText("knowledge.ingestRetainedRequiresProcessing")).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "OK" }));
    await waitFor(() => expect(onFinish).toHaveBeenCalledWith(["doc"]));
    expect(mocks.create).toHaveBeenCalledWith("library", expect.objectContaining({ items: [expect.objectContaining({ task: expect.objectContaining({ task_type: "TASK_TYPE_PARSE_UPLOADED", document_ids: ["doc"] }) })] }), expect.anything());
  });
});
