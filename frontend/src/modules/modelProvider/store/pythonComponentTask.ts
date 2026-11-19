import { create } from "zustand";
import { message } from "antd";
import i18n from "@/i18n";
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
  const name = i18n.t(id === "cluster" ? "clusterComponent.name" : "ragComponent.name");
  currentTask = (async () => {
    let restarting = false;
    let installed: PythonComponentStatus | undefined;
    try {
      if (!restartOnly) {
        progress("installing", i18n.t("ragComponent.installingMessage", { name }));
        installed = await installPythonComponent(id, controller.signal);
      }
      download = undefined;
      setTransientRequestErrorsSuppressed(true);
      restarting = true;
      progress("restarting", i18n.t("ragComponent.restartingMessage"));
      const result = await restartRuntime({ reload: false });
      if (!result.ok && result.reason === "unavailable" && installed?.installMethod === "source") {
        usePythonComponentTask.setState({ phase: "idle", error: "" });
        window.dispatchEvent(new Event(PYTHON_COMPONENTS_CHANGED_EVENT));
        message.info({ key: notificationKey, content: "RAG 依赖已安装。请执行 make local-down，再执行 make local-up LAZYMIND_RAG_ENABLED=false 完成启用。", duration: 12 });
        return;
      }
      if (!result.ok) throw result.error || new Error(i18n.t("ragComponent.restartFailedMessage"));
      progress("verifying", i18n.t("ragComponent.verifyingMessage", { name }));
      const items = await getPythonComponents();
      if (!items.some(item => item.id === id && item.active)) {
        throw new Error(i18n.t("ragComponent.verifyFailedMessage", { name }));
      }
      usePythonComponentTask.setState({ phase: "success", error: "" });
      window.dispatchEvent(new Event(PYTHON_COMPONENTS_CHANGED_EVENT));
      message.success({ key: notificationKey, content: i18n.t("ragComponent.successMessage", { name }), duration: 5 });
    } catch (error) {
      if (controller.signal.aborted) {
        usePythonComponentTask.setState({ phase: "idle", error: "" });
        message.info({ key: notificationKey, content: i18n.t("ragComponent.canceledMessage"), duration: 3 });
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
