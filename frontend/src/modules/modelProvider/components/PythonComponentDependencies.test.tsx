import { RAGComponentProvider } from "../contexts/RAGComponentContext";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { ConfigProvider, message } from "antd";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
const mocks = vi.hoisted(() => ({ get: vi.fn(), install: vi.fn(), restart: vi.fn(), suppress: vi.fn() }));
vi.mock("../api/systemDependencies", () => ({ getPythonComponents: mocks.get, installPythonComponent: mocks.install }));
vi.mock("@/runtime/desktopBridge", () => ({ restartRuntime: mocks.restart }));
vi.mock("@/components/request", () => ({ setTransientRequestErrorsSuppressed: mocks.suppress }));
import PythonComponentDependencies, { RAGComponentNotice } from "./PythonComponentDependencies";
import { runPythonComponentTask, usePythonComponentTask } from "../store/pythonComponentTask";
const missing = { id: "rag", installed: false, active: false, restartRequired: false,
  installSupported: true, installing: false, filename: "rag-test.zip", url: "https://modelscope.cn/datasets/test/resolve/master/rag-test.zip", sizeBytes: 1048576 };
const installed = { ...missing, installed: true, restartRequired: true };
const enabled = { ...installed, active: true, restartRequired: false };
const mount = () => render(<ConfigProvider theme={{ token: { motion: false } }}><MemoryRouter><PythonComponentDependencies /></MemoryRouter></ConfigProvider>);
async function begin() {
  fireEvent.click(await screen.findByRole("button", { name: "安装组件" }));
  fireEvent.click(screen.getByRole("button", { name: /下载并安装/ }));
}
beforeEach(() => {
  vi.resetAllMocks();
  usePythonComponentTask.setState({ phase: "idle", id: null, error: "" });
  mocks.get.mockResolvedValue([missing]);
  mocks.install.mockResolvedValue(installed);
  mocks.restart.mockImplementation(async () => { mocks.get.mockResolvedValue([enabled]); return { ok: true }; });
  vi.spyOn(message, "success");
  vi.spyOn(message, "error");
});
afterEach(() => { cleanup(); message.destroy(); vi.restoreAllMocks(); });

it("downloads in the background across navigation, deduplicates clicks, and waits for restart plus activation", async () => {
  let finishDownload!: () => void;
  let finishRestart!: () => void;
  mocks.install.mockImplementation(() => new Promise(resolve => {
    finishDownload = () => { mocks.get.mockResolvedValue([installed]); resolve(installed); };
  }));
  mocks.restart.mockImplementation(() => new Promise(resolve => {
    finishRestart = () => { mocks.get.mockResolvedValue([enabled]); resolve({ ok: true }); };
  }));
  const view = mount();
  fireEvent.click(await screen.findByRole("button", { name: "安装组件" }));
  expect(screen.getByLabelText("组件下载来源")).toHaveTextContent(missing.url);
  expect(screen.queryByRole("textbox")).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: /下载并安装/ }));
  await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
  const task = runPythonComponentTask("rag");
  expect(mocks.install).toHaveBeenCalledOnce();
  expect(screen.getByRole("button", { name: /后台安装中/ })).toBeDisabled();
  view.unmount();
  expect(mocks.install.mock.calls[0][1].aborted).toBe(false);
  mount();
  expect(await screen.findByRole("button", { name: /后台安装中/ })).toBeDisabled();
  await act(async () => finishDownload());
  await waitFor(() => expect(mocks.restart).toHaveBeenCalledWith({ reload: false }));
  expect(screen.getByRole("button", { name: /正在重启服务/ })).toBeDisabled();
  expect(message.success).not.toHaveBeenCalled();
  expect(mocks.suppress).toHaveBeenCalledWith(true);
  await act(async () => { finishRestart(); await task; });
  expect(await screen.findByText("已启用")).toBeInTheDocument();
  expect(message.success).toHaveBeenCalledOnce();
  expect(mocks.suppress).toHaveBeenLastCalledWith(false);
});

it("reports a download failure once and allows retry without restarting", async () => {
  mocks.install.mockRejectedValue(new Error("checksum mismatch"));
  mount(); await begin();
  await waitFor(() => expect(usePythonComponentTask.getState().phase).toBe("error"));
  expect(screen.getByRole("button", { name: "安装组件" })).toBeEnabled();
  expect(mocks.restart).not.toHaveBeenCalled();
  expect(message.error).toHaveBeenCalledOnce();
  expect(message.success).not.toHaveBeenCalled();
});

it("reports restart failure once and retries activation without downloading again", async () => {
  mocks.install.mockImplementation(async () => { mocks.get.mockResolvedValue([installed]); return installed; });
  mocks.restart.mockResolvedValueOnce({ ok: false, reason: "failed", error: new Error("restart timed out") });
  mount(); await begin();
  const retry = await screen.findByRole("button", { name: "重试启用组件" });
  expect(message.error).toHaveBeenCalledOnce();
  expect(message.success).not.toHaveBeenCalled();
  expect(mocks.suppress).toHaveBeenLastCalledWith(false);
  fireEvent.click(retry);
  await waitFor(() => expect(usePythonComponentTask.getState().phase).toBe("success"));
  expect(mocks.install).toHaveBeenCalledOnce();
  expect(mocks.restart).toHaveBeenCalledTimes(2);
});

it("does not announce success if restart returns before the component is active", async () => {
  mocks.get.mockResolvedValue([installed]);
  mocks.restart.mockResolvedValue({ ok: true });
  mount();
  fireEvent.click(await screen.findByRole("button", { name: "重试启用组件" }));
  await waitFor(() => expect(usePythonComponentTask.getState().phase).toBe("error"));
  expect(message.success).not.toHaveBeenCalled();
  expect(message.error).toHaveBeenCalledOnce();
  expect(mocks.install).not.toHaveBeenCalled();
});

it("keeps cancellation explicit after dismissing the installation modal", async () => {
  mocks.install.mockImplementation((_id, signal) => new Promise((_resolve, reject) => {
    signal.addEventListener("abort", () => reject(new Error("cancelled")));
  }));
  mount(); await begin();
  await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
  fireEvent.keyDown(document.body, { key: "Escape" });
  expect(mocks.install.mock.calls[0][1].aborted).toBe(false);
  fireEvent.click(screen.getByRole("button", { name: "取消下载" }));
  await waitFor(() => expect(usePythonComponentTask.getState().phase).toBe("idle"));
  expect(mocks.install.mock.calls[0][1].aborted).toBe(true);
  expect(mocks.restart).not.toHaveBeenCalled();
  expect(message.error).not.toHaveBeenCalled();
});

it("shows an install link for an unavailable knowledge component", async () => {
  render(<MemoryRouter><RAGComponentProvider><RAGComponentNotice /></RAGComponentProvider></MemoryRouter>);
  expect(await screen.findByRole("link", { name: "安装组件" })).toHaveAttribute("href", "/settings?section=system_tools#python-rag-dependency");
});
it("does not allow installing when the configured source is missing", async () => {
  mocks.get.mockResolvedValue([{ ...missing, url: undefined }]);
  mount();
  fireEvent.click(await screen.findByRole("button", { name: "安装组件" }));
  expect(screen.getByRole("button", { name: /下载并安装/ })).toBeDisabled();
});
it("does not allow a duplicate download already running on the server", async () => {
  mocks.get.mockResolvedValue([{ ...missing, installing: true }]);
  mount();
  expect(await screen.findByRole("button", { name: "安装中" })).toBeDisabled();
  expect(mocks.install).not.toHaveBeenCalled();
});
