import { create } from "zustand";
import { message } from "antd";
import { setTransientRequestErrorsSuppressed } from "@/components/request";
import { restartRuntime } from "@/runtime/desktopBridge";
import { getPythonComponents, installPythonComponent, type PythonComponentStatus } from "../api/systemDependencies";

export const PYTHON_COMPONENTS_CHANGED_EVENT = "lazymind:python-components-changed";
type Phase = "idle" | "installing" | "restarting" | "verifying" | "success" | "error";
interface TaskState { phase: Phase; id: PythonComponentStatus["id"] | null; error: string }
export const usePythonComponentTask = create<TaskState>(() => ({ phase: "idle", id: null, error: "" }));
export const pythonComponentTaskBusy = (phase: Phase) => ["installing", "restarting", "verifying"].includes(phase);
const notificationKey = "python-component-install";
let currentTask: Promise<void> | null = null;
let download: AbortController | undefined;

export function pythonComponentErrorText(error: unknown): string {
  const value = error as { response?: { data?: { message?: string; error?: string; data?: { detail?: string } } }; message?: string } | null;
  return value?.response?.data?.data?.detail || value?.response?.data?.message || value?.response?.data?.error || value?.message || "组件操作失败";
}

function progress(phase: Phase, content: string) {
  usePythonComponentTask.setState({ phase });
  message.loading({ key: notificationKey, content, duration: 0 });
}

// Kept outside the settings component so route changes do not cancel installation.
export function runPythonComponentTask(id: PythonComponentStatus["id"], restartOnly = false): Promise<void> {
  if (currentTask) return currentTask;
  const controller = new AbortController();
  download = restartOnly ? undefined : controller;
  usePythonComponentTask.setState({ id, error: "", phase: restartOnly ? "restarting" : "installing" });
  currentTask = (async () => {
    let restarting = false;
    try {
      if (!restartOnly) {
        progress("installing", "正在后台下载并安装本地知识库组件，可以继续浏览其他页面。");
        await installPythonComponent(id, controller.signal);
      }
      download = undefined;
      setTransientRequestErrorsSuppressed(true);
      restarting = true;
      progress("restarting", "组件已安装，正在自动重启本地服务…");
      const result = await restartRuntime({ reload: false });
      if (!result.ok) throw result.error || new Error("无法自动重启本地服务，请重试启用组件。");
      progress("verifying", "本地服务已重启，正在确认知识库组件已启用…");
      const items = await getPythonComponents();
      if (!items.some(item => item.id === id && item.active)) {
        throw new Error("组件已安装，但本地知识库服务尚未启用，请重试启用组件。");
      }
      usePythonComponentTask.setState({ phase: "success", error: "" });
      window.dispatchEvent(new Event(PYTHON_COMPONENTS_CHANGED_EVENT));
      message.success({ key: notificationKey, content: "本地知识库组件安装成功，服务已重启并启用。", duration: 5 });
    } catch (error) {
      if (controller.signal.aborted) {
        usePythonComponentTask.setState({ phase: "idle", error: "" });
        message.info({ key: notificationKey, content: "已取消组件下载。", duration: 3 });
      } else {
        const detail = pythonComponentErrorText(error);
        usePythonComponentTask.setState({ phase: "error", error: detail });
        message.error({ key: notificationKey, content: detail, duration: 8 });
      }
    } finally {
      if (restarting) setTransientRequestErrorsSuppressed(false);
      download = undefined;
      currentTask = null;
    }
  })();
  return currentTask;
}

export function cancelPythonComponentDownload() {
  if (usePythonComponentTask.getState().phase === "installing") download?.abort();
}
