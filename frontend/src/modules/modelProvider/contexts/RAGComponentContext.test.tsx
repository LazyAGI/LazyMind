import { act, cleanup, renderHook, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { ReactNode } from "react";
import { usePythonComponentTask } from "../store/pythonComponentTask";
import { RAGComponentProvider, useRAGComponent } from "./RAGComponentContext";
const mocks = vi.hoisted(() => ({ get: vi.fn(), mode: "desktop" }));
vi.mock("@/components/request", () => ({ setTransientRequestErrorsSuppressed: vi.fn() }));
vi.mock("@/runtime/desktopBridge", () => ({ restartRuntime: vi.fn() }));
vi.mock("../api/systemDependencies", () => ({ getPythonComponents: mocks.get }));
vi.mock("@/runtime/mode", () => ({
  getRuntimeMode: () => mocks.mode,
  isLocalLikeRuntimeMode: (mode: string) => mode !== "cloud",
}));
const wrapper = ({ children }: { children: ReactNode }) =>
  <MemoryRouter><RAGComponentProvider>{children}</RAGComponentProvider></MemoryRouter>;
beforeEach(() => { vi.resetAllMocks(); mocks.mode = "desktop"; });
afterEach(cleanup);

it.each([
  { installed: false, installing: false, restartRequired: false },
  { installed: false, installing: true, restartRequired: false },
  { installed: true, installing: false, restartRequired: true },
])("keeps creation unavailable until RAG is active: %j", async (status) => {
  mocks.get.mockResolvedValue([{ id: "rag", active: false, ...status }]);
  const { result } = renderHook(useRAGComponent, { wrapper });
  expect(result.current.availability).toBe("checking");
  await waitFor(() => expect(result.current.availability).toBe("missing"));
  mocks.get.mockResolvedValue([{ id: "rag", installed: true, active: true }]);
  act(() => { window.dispatchEvent(new Event("focus")); });
  await waitFor(() => expect(result.current.availability).toBe("ready"));
});

it("keeps an installed RAG component available during an independent clustering download", async () => {
  mocks.get.mockResolvedValue([{ id: "rag", active: true }, { id: "cluster", active: false }]);
  const { result, unmount } = renderHook(useRAGComponent, { wrapper });
  try {
    await waitFor(() => expect(result.current.availability).toBe("ready"));
    act(() => usePythonComponentTask.setState({ id: "cluster", phase: "installing" }));
    expect(result.current.availability).toBe("ready");
    act(() => usePythonComponentTask.setState({ phase: "restarting" }));
    expect(result.current.availability).toBe("checking");
  } finally {
    unmount();
    usePythonComponentTask.setState({ id: null, phase: "idle" });
  }
});

it("keeps creation unavailable when local status cannot be checked and supports retry", async () => {
  mocks.get.mockRejectedValue(new Error("offline"));
  const { result } = renderHook(useRAGComponent, { wrapper });
  await waitFor(() => expect(result.current.availability).toBe("error"));
  mocks.get.mockResolvedValue([{ id: "rag", active: true }]);
  act(() => { result.current.refresh(); });
  await waitFor(() => expect(result.current.availability).toBe("ready"));
});

it("preserves cloud compatibility when the optional component API is unavailable", async () => {
  mocks.mode = "cloud";
  mocks.get.mockRejectedValue(new Error("not found"));
  const { result } = renderHook(useRAGComponent, { wrapper });
  await waitFor(() => expect(result.current.availability).toBe("ready"));
});

it("does not enable creation from an older status request after a newer missing result", async () => {
  let finishOld!: (items: unknown[]) => void;
  mocks.get.mockImplementationOnce(() => new Promise(resolve => { finishOld = resolve; }))
    .mockResolvedValue([{ id: "rag", active: false }]);
  const { result } = renderHook(useRAGComponent, { wrapper });
  act(() => { result.current.refresh(); });
  await waitFor(() => expect(result.current.availability).toBe("missing"));
  await act(async () => finishOld([{ id: "rag", active: true }]));
  expect(result.current.availability).toBe("missing");
});
