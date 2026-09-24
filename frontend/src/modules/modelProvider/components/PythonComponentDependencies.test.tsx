import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
const mocks = vi.hoisted(() => ({ get: vi.fn(), install: vi.fn(), restart: vi.fn() }));
vi.mock("../api/systemDependencies", () => ({ getPythonComponents: mocks.get, installPythonComponent: mocks.install }));
vi.mock("@/runtime/desktopBridge", () => ({ restartRuntime: mocks.restart }));
import PythonComponentDependencies, { RAGComponentNotice } from "./PythonComponentDependencies";
const missing = { id: "rag", installed: false, active: false, restartRequired: false,
  installSupported: true, installing: false, filename: "rag-test.zip", url: "https://modelscope.cn/datasets/test/resolve/master/rag-test.zip", sizeBytes: 1048576 };
beforeEach(() => { vi.resetAllMocks(); mocks.get.mockResolvedValue([missing]); });
afterEach(cleanup);
it("shows the configured source without a URL editor and requires an explicit restart", async () => {
  const installed = { ...missing, installed: true, restartRequired: true };
  mocks.install.mockImplementation(async () => { mocks.get.mockResolvedValue([installed]); return installed; });
  mocks.restart.mockResolvedValue({ ok: true });
  render(<MemoryRouter><PythonComponentDependencies /></MemoryRouter>);
  fireEvent.click(await screen.findByRole("button", { name: "安装组件" }));
  expect(screen.getByText("rag-test.zip")).toBeInTheDocument();
  const confirm = screen.getByRole("button", { name: /下载并安装/ });
  expect(confirm).toBeEnabled();
  expect(screen.getByLabelText("组件下载来源")).toHaveTextContent(missing.url);
  expect(screen.queryByRole("textbox")).not.toBeInTheDocument();
  fireEvent.click(confirm);
  await waitFor(() => expect(mocks.install).toHaveBeenCalledWith("rag", expect.any(AbortSignal)));
  const restart = await screen.findByRole("button", { name: "重启本地服务" });
  expect(mocks.restart).not.toHaveBeenCalled();
  fireEvent.click(restart);
  await waitFor(() => expect(mocks.restart).toHaveBeenCalledOnce());
});
it("keeps installation available after a download fails", async () => {
  mocks.install.mockRejectedValue(new Error("checksum mismatch"));
  render(<MemoryRouter><PythonComponentDependencies /></MemoryRouter>);
  fireEvent.click(await screen.findByRole("button", { name: "安装组件" }));
  fireEvent.click(screen.getByRole("button", { name: /下载并安装/ }));
  await waitFor(() => expect(mocks.install).toHaveBeenCalledOnce());
  await waitFor(() => expect(screen.getByRole("button", { name: /下载并安装/ })).toBeEnabled());
  expect(mocks.restart).not.toHaveBeenCalled();
});
it("shows an install link for an unavailable knowledge component", async () => {
  render(<MemoryRouter><RAGComponentNotice /></MemoryRouter>);
  expect(await screen.findByRole("link", { name: "安装组件" })).toHaveAttribute("href", "/settings?section=system_tools#python-rag-dependency");
});

it("does not allow installing when the configured source is missing", async () => {
  mocks.get.mockResolvedValue([{ ...missing, url: undefined }]);
  render(<MemoryRouter><PythonComponentDependencies /></MemoryRouter>);
  fireEvent.click(await screen.findByRole("button", { name: "安装组件" }));
  expect(screen.getByRole("button", { name: /下载并安装/ })).toBeDisabled();
  expect(screen.queryByRole("textbox")).not.toBeInTheDocument();
});
it("cancels a download and allows retrying the configured source", async () => {
  mocks.install.mockImplementation((_id, signal) => new Promise((_resolve, reject) => {
    signal.addEventListener("abort", () => reject(new Error("cancelled")));
  }));
  render(<MemoryRouter><PythonComponentDependencies /></MemoryRouter>);
  fireEvent.click(await screen.findByRole("button", { name: "安装组件" }));
  fireEvent.click(screen.getByRole("button", { name: /下载并安装/ }));
  fireEvent.click(await screen.findByRole("button", { name: /取消下载/ }));
  await waitFor(() => expect(mocks.install.mock.calls[0][1].aborted).toBe(true));
  await waitFor(() => expect(screen.getByRole("button", { name: /下载并安装/ })).toBeEnabled());
});

it("does not cancel an active download by clicking the backdrop or pressing Escape", async () => {
  mocks.install.mockImplementation((_id, signal) => new Promise((_resolve, reject) => {
    signal.addEventListener("abort", () => reject(new Error("cancelled")));
  }));
  render(<MemoryRouter><PythonComponentDependencies /></MemoryRouter>);
  fireEvent.click(await screen.findByRole("button", { name: "安装组件" }));
  fireEvent.click(screen.getByRole("button", { name: /下载并安装/ }));
  await waitFor(() => expect(mocks.install).toHaveBeenCalledOnce());
  const signal = mocks.install.mock.calls[0][1];
  const backdrop = document.querySelector(".ant-modal-wrap")!;
  fireEvent.mouseDown(backdrop);
  fireEvent.mouseUp(backdrop);
  fireEvent.click(backdrop);
  fireEvent.keyDown(backdrop, { key: "Escape", keyCode: 27 });
  expect(signal.aborted).toBe(false);
  expect(screen.getByRole("button", { name: /取消下载/ })).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "安装组件" })).toBeDisabled();
  fireEvent.click(screen.getByRole("button", { name: /取消下载/ }));
  await waitFor(() => expect(signal.aborted).toBe(true));
});
