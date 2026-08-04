import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, render, screen, waitFor } from "@testing-library/react";
import StateGraphModal, { dispatchGraphRefresh, PLUGIN_GRAPH_REFRESH_EVENT } from "./index";

vi.mock("./index.scss", () => ({}));

const getMock = vi.hoisted(() => vi.fn());
vi.mock("@/components/request", () => ({
  axiosInstance: { get: getMock },
  BASE_URL: "http://mock-base",
  localizeErrorCode: (code: string) => `error:${code}`,
}));

vi.mock("./StateGraphView", () => ({
  default: ({ data }: { data: { nodes: { id: string }[] } }) => (
    <div data-testid="state-graph-view">{data.nodes.length} nodes</div>
  ),
}));

function projectionResponse() {
  return {
    data: {
      data: {
        projection: {
          past: ["step-1"],
          current: ["step-2"],
          nodes: {
            "step-1": { execution: "succeeded", readiness: "none", branch: "none" },
            "step-2": { execution: "running", readiness: "none", branch: "none" },
          },
          edges: [{ from: "step-1", to: "step-2", state: "active" }],
          completed: false,
        },
        graph: {
          static_order: ["step-1", "step-2"],
          nodes: {
            "step-1": { id: "step-1", label: "Step One" },
            "step-2": { id: "step-2", label: "Step Two" },
          },
        },
      },
    },
  };
}

describe("StateGraphModal", () => {
  beforeEach(() => {
    getMock.mockReset();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("does not fetch or render when closed", () => {
    render(
      <StateGraphModal open={false} onClose={vi.fn()} sessionId="s1" pluginId="p1" />,
    );
    expect(getMock).not.toHaveBeenCalled();
  });

  it("fetches the session projection and renders the graph when opened", async () => {
    getMock.mockResolvedValue(projectionResponse());

    render(<StateGraphModal open onClose={vi.fn()} sessionId="s1" pluginId="p1" />);

    await waitFor(() => expect(getMock).toHaveBeenCalledTimes(1));
    expect(getMock).toHaveBeenCalledWith(
      "http://mock-base/api/core/plugin-sessions/s1/projection",
      { silentError: true },
    );
    await waitFor(() =>
      expect(screen.getByTestId("state-graph-view")).toBeInTheDocument(),
    );
  });

  it("falls back to rendering fallbackSteps when the projection request fails", async () => {
    getMock.mockRejectedValue(new Error("network down"));

    render(
      <StateGraphModal
        open
        onClose={vi.fn()}
        sessionId="s1"
        pluginId="p1"
        fallbackSteps={[{ step_id: "step-a", status: "succeeded" }]}
      />,
    );

    await waitFor(() =>
      expect(screen.getByTestId("state-graph-view")).toBeInTheDocument(),
    );
  });

  it("shows a localized error message when the request fails and there are no fallback steps", async () => {
    getMock.mockRejectedValue(new Error("network down"));

    render(<StateGraphModal open onClose={vi.fn()} sessionId="s1" pluginId="p1" />);

    await waitFor(() => expect(screen.getByText("error:2000509")).toBeInTheDocument());
  });

  it("re-fetches on a matching plugin:graph:refresh event when liveRefresh is enabled", async () => {
    getMock.mockResolvedValue(projectionResponse());

    render(
      <StateGraphModal
        open
        onClose={vi.fn()}
        sessionId="s1"
        pluginId="p1"
        liveRefresh
        conversationId="conv-1"
      />,
    );

    await waitFor(() => expect(getMock).toHaveBeenCalledTimes(1));

    act(() => {
      dispatchGraphRefresh("conv-1");
    });

    await waitFor(() => expect(getMock).toHaveBeenCalledTimes(2));
  });

  it("ignores refresh events for a different conversation", async () => {
    getMock.mockResolvedValue(projectionResponse());

    render(
      <StateGraphModal
        open
        onClose={vi.fn()}
        sessionId="s1"
        pluginId="p1"
        liveRefresh
        conversationId="conv-1"
      />,
    );

    await waitFor(() => expect(getMock).toHaveBeenCalledTimes(1));

    act(() => {
      window.dispatchEvent(
        new CustomEvent(PLUGIN_GRAPH_REFRESH_EVENT, { detail: { conversationId: "other" } }),
      );
    });

    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(getMock).toHaveBeenCalledTimes(1);
  });
});
