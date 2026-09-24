import { act, cleanup, renderHook, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { WorkflowVersionContent } from "../workflowDraftApi";

const mocks = vi.hoisted(() => ({ params: { workflowRef: "first" }, list: vi.fn(), read: vi.fn(), navigate: vi.fn() }));
vi.mock("react-router-dom", () => ({ useParams: () => mocks.params, useNavigate: () => mocks.navigate }));
vi.mock("react-i18next", () => ({ useTranslation: () => ({ t: (key: string) => key }) }));
vi.mock("../workflowDraftApi", () => ({ listWorkflowVersions: mocks.list, getWorkflowVersion: mocks.read }));
import { usePublishedWorkflowDetail } from "./usePublishedWorkflowDetail";

beforeEach(() => {
  mocks.params.workflowRef = "first";
  mocks.list.mockReset().mockResolvedValue([{ current: true, revision_id: "latest" }]);
  mocks.read.mockReset();
});
afterEach(cleanup);

describe("published workflow loading", () => {
  it("ignores the previous workflow response after changing routes", async () => {
    let finish!: (value: Partial<WorkflowVersionContent>) => void;
    mocks.read.mockReturnValueOnce(new Promise((resolve) => { finish = resolve })).mockResolvedValueOnce({ revision_id: "second", workflow_yaml_content: "name: second", state_yaml_content: "" });
    const { result, rerender } = renderHook(() => usePublishedWorkflowDetail());
    await waitFor(() => expect(mocks.read).toHaveBeenCalledOnce());
    mocks.params.workflowRef = "second";
    rerender();
    await waitFor(() => expect(result.current.content?.revision_id).toBe("second"));
    await act(async () => finish({ revision_id: "first", workflow_yaml_content: "name: first" }));
    expect(result.current.content?.revision_id).toBe("second");
  });

  it("retries a failed version lookup only when requested", async () => {
    mocks.list.mockRejectedValueOnce(new Error("offline"));
    mocks.read.mockResolvedValue({ revision_id: "latest", workflow_yaml_content: "name: recovered", state_yaml_content: "" });
    const { result } = renderHook(() => usePublishedWorkflowDetail());
    await waitFor(() => expect(result.current.error).toBe(true));
    expect(mocks.read).not.toHaveBeenCalled();
    act(() => result.current.reload());
    await waitFor(() => expect(result.current.content?.revision_id).toBe("latest"));
    expect(result.current.error).toBe(false);
  });
});
