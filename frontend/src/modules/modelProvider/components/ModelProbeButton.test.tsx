import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { ConfigProvider } from "antd";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ModelProbeButton } from "./ModelProbeButton";

const mocks = vi.hoisted(() => ({ probe: vi.fn() }));
vi.mock("../modelProbe", () => ({ probeModel: mocks.probe, probeFailureCode: () => "connection_error" }));
vi.mock("react-i18next", () => ({ useTranslation: () => ({ t: (key: string, values?: { name?: string; elapsed?: number; dimensions?: number }) =>
  values?.name ? `${key}:${values.name}` : values?.elapsed !== undefined ? `${key}:${values.elapsed}` :
    values?.dimensions !== undefined ? `${key}:${values.dimensions}` : key }) }));

const identity = { providerId: "supplier", groupId: "group", modelId: "model" };
function setup(configurationKey = "config-1", disabledReason?: string) {
  const pending = vi.fn();
  const props = { ...identity, modelName: "qwen-test", configurationKey, disabledReason, onPendingChange: pending };
  const view = render(<ConfigProvider theme={{ token: { motion: false } }}><ModelProbeButton {...props} /></ConfigProvider>);
  return { ...view, props, pending };
}
beforeEach(() => { vi.clearAllMocks(); mocks.probe.mockResolvedValue({ status: "passed", code: "ok", model_type: "llm", elapsed_ms: 24 }); });

describe("model inference probe", () => {
  it("announces measured embedding dimensions in the result tooltip", async () => {
    mocks.probe.mockResolvedValueOnce({ status: "passed", code: "ok", model_type: "embed", elapsed_ms: 24, embedding_dimensions: 1024 });
    setup();
    fireEvent.click(screen.getByRole("button", { name: "modelProvider.probe.action:qwen-test" }));
    const button = await screen.findByRole("button", { name: "modelProvider.probe.passed:qwen-test" });
    fireEvent.mouseEnter(button);
    expect(await screen.findByRole("tooltip")).toHaveTextContent("modelProvider.probe.embeddingDimensions:1024");
  });
  it.each([[2999, "normal"], [3000, "slow"], [3001, "slow"]] as const)("shows passed probes at %i ms with %s latency tone", async (elapsed, tone) => {
    mocks.probe.mockResolvedValueOnce({ status: "passed", code: "ok", model_type: "llm", elapsed_ms: elapsed });
    setup();
    fireEvent.click(screen.getByRole("button", { name: "modelProvider.probe.action:qwen-test" }));
    const button = await screen.findByRole("button", { name: "modelProvider.probe.passed:qwen-test" });
    expect(button).toHaveAttribute("data-probe-status", "passed");
    expect(button).toHaveAttribute("data-probe-latency", tone);
    expect(button.querySelector(".anticon-check-circle")).toBeInTheDocument();
  });

  it.each(["failed", "unsupported", "accepted"] as const)("retains %s semantics beyond the slow-probe threshold", async (status) => {
    mocks.probe.mockResolvedValueOnce({ status, code: status === "failed" ? "quota_exceeded" : status === "unsupported" ? "unsupported_type" : "accepted", model_type: "llm", elapsed_ms: 3001 });
    setup();
    fireEvent.click(screen.getByRole("button", { name: "modelProvider.probe.action:qwen-test" }));
    const button = await screen.findByRole("button", { name: `modelProvider.probe.${status}:qwen-test` });
    expect(button).toHaveAttribute("data-probe-status", status);
    expect(button).not.toHaveAttribute("data-probe-latency");
  });

  it("runs one request per model, shows a result and does not expose raw data", async () => {
    let resolve!: (value: unknown) => void;
    mocks.probe.mockReturnValueOnce(new Promise((done) => { resolve = done; }));
    const { pending } = setup();
    const button = screen.getByRole("button", { name: "modelProvider.probe.action:qwen-test" });
    act(() => { fireEvent.click(button); fireEvent.click(button); });
    expect(mocks.probe).toHaveBeenCalledTimes(1);
    expect(mocks.probe).toHaveBeenCalledWith(identity, expect.any(AbortSignal));
    expect(button).toBeDisabled();
    expect(button).toHaveAttribute("aria-busy", "true");
    expect(pending).toHaveBeenCalledWith(true);
    await act(async () => resolve({ status: "passed", code: "ok", model_type: "llm", elapsed_ms: 24, message: "secret should not render" }));
    expect(button).toHaveAttribute("data-probe-status", "passed");
    expect(button).toHaveAttribute("aria-label", "modelProvider.probe.passed:qwen-test");
    expect(button).not.toBeDisabled();
    expect(pending).toHaveBeenLastCalledWith(false);
    fireEvent.mouseEnter(button);
    expect(await screen.findByRole("tooltip")).toHaveTextContent("modelProvider.probe.elapsed:24");
    expect(screen.queryByText(/secret should/)).not.toBeInTheDocument();
  });

  it("clears and aborts the old probe when the endpoint or enabled key changes", async () => {
    let resolve!: (value: unknown) => void;
    mocks.probe.mockReturnValueOnce(new Promise((done) => { resolve = done; }));
    const { rerender, props } = setup();
    fireEvent.click(screen.getByRole("button", { name: "modelProvider.probe.action:qwen-test" }));
    const signal = mocks.probe.mock.calls[0][1] as AbortSignal;
    rerender(<ConfigProvider theme={{ token: { motion: false } }}><ModelProbeButton {...props} configurationKey="config-2" /></ConfigProvider>);
    expect(signal.aborted).toBe(true);
    await act(async () => resolve({ status: "passed", code: "ok", elapsed_ms: 5 }));
    expect(screen.getByRole("button", { name: "modelProvider.probe.action:qwen-test" })).not.toHaveAttribute("data-probe-status");
  });

  it.each(["accepted", "unsupported", "failed"] as const)("names %s distinctly instead of calling it inference success", async (status) => {
    mocks.probe.mockResolvedValueOnce({ status, code: status === "unsupported" ? "unsupported_type" : status === "failed" ? "quota_exceeded" : "accepted", model_type: "text2video", elapsed_ms: 17 });
    setup();
    fireEvent.click(screen.getByRole("button", { name: "modelProvider.probe.action:qwen-test" }));
    const button = await screen.findByRole("button", { name: `modelProvider.probe.${status}:qwen-test` });
    expect(button).toHaveAttribute("data-probe-status", status);
  });

  it("keeps transport errors generic and permits an explicit retry", async () => {
    mocks.probe.mockRejectedValueOnce(new Error("https://host.test/?api_key=raw-secret"));
    setup();
    fireEvent.click(screen.getByRole("button", { name: "modelProvider.probe.action:qwen-test" }));
    const failed = await screen.findByRole("button", { name: "modelProvider.probe.failed:qwen-test" });
    fireEvent.mouseEnter(failed);
    expect(await screen.findByRole("tooltip")).toHaveTextContent("modelProvider.probe.reason.connection_error");
    expect(screen.queryByText(/raw-secret/)).not.toBeInTheDocument();
    fireEvent.click(failed);
    await screen.findByRole("button", { name: "modelProvider.probe.passed:qwen-test" });
    expect(mocks.probe).toHaveBeenCalledTimes(2);
  });

  it("does not start a request for a disabled model", async () => {
    setup("config-1", "Model type is not supported");
    const button = screen.getByRole("button", { name: "modelProvider.probe.action:qwen-test" });
    expect(button).toBeDisabled();
    fireEvent.click(button);
    await waitFor(() => expect(mocks.probe).not.toHaveBeenCalled());
  });
});
