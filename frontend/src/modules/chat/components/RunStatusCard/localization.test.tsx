import { fireEvent, render, screen } from "@testing-library/react";
import { createInstance } from "i18next";
import { I18nextProvider } from "react-i18next";
import { beforeAll, describe, expect, it, vi } from "vitest";
import { RunTerminalReasonEnum, RunTerminalStatusEnum } from "@/api/generated/core-client";
import zhCN from "@/i18n/locales/zh-CN";
import { CHAT_OPEN_MODEL_SELECTOR_EVENT } from "@/modules/chat/constants/chat";
import {
  applyChatStreamFailure,
  MODEL_FAILURE_CODES,
  parseCoreChatStreamError,
} from "@/modules/chat/utils/chatStreamError";
import { buildChatMessageListFromHistory } from "@/modules/chat/utils/message";
import RunStatusCard, { type RunTerminalView } from "./index";

const i18n = createInstance();
beforeAll(async () => {
  await i18n.init({ lng: "zh-CN", fallbackLng: false, resources: {
    "zh-CN": { translation: zhCN },
  } });
});

function renderFailure(terminal: RunTerminalView, onRetry = vi.fn()) {
  return render(<I18nextProvider i18n={i18n}>
    <RunStatusCard terminal={terminal} conversationId="test-chat" onRetry={onRetry} />
  </I18nextProvider>);
}

describe("Chinese model error recovery", () => {
  it.each([
    { status: RunTerminalStatusEnum.Failed, reason: RunTerminalReasonEnum.ModelFailure, code: "model_configuration", partial_output: false },
    { status: RunTerminalStatusEnum.Failed, reason: RunTerminalReasonEnum.ModelFailure, code: "not_found", partial_output: false },
    { status: RunTerminalStatusEnum.Failed, reason: RunTerminalReasonEnum.RuntimeFailure, code: "protocol_error", partial_output: true, model_invoked: false },
    { status: RunTerminalStatusEnum.Failed, reason: RunTerminalReasonEnum.RuntimeFailure, code: "state_store_unavailable", partial_output: false },
    { status: RunTerminalStatusEnum.Failed, reason: RunTerminalReasonEnum.ModelFailure, code: "future_provider_error", partial_output: false },
    { status: RunTerminalStatusEnum.Interrupted, reason: RunTerminalReasonEnum.ModelIncomplete, code: "length", partial_output: true },
    { status: RunTerminalStatusEnum.Cancelled, reason: RunTerminalReasonEnum.UserCancelled, partial_output: false },
  ])("keeps HTTP and refreshed text/actions identical for $reason/$code", (terminal) => {
    const mapped = parseCoreChatStreamError({ code: 2000000, message: "private error", data: { run_terminal: terminal } }, 502)!;
    const immediate = applyChatStreamFailure([{ role: "user", delta: "test question" }], "assistant", mapped.terminal);
    const { rerender } = renderFailure(immediate[1].run_terminal);
    const copy = screen.getByRole("alert").textContent;
    const actions = screen.queryAllByRole("button").map(button => button.textContent);
    const links = screen.queryAllByRole("link").map(link => [link.textContent, link.getAttribute("href")]);
    const restored = buildChatMessageListFromHistory([{ id: "contract-history", query: "test question", run_status: terminal.status, run_terminal: terminal }]);
    rerender(<I18nextProvider i18n={i18n}><RunStatusCard terminal={restored[1].run_terminal} conversationId="test-chat" onRetry={vi.fn()} /></I18nextProvider>);
    expect(screen.getByRole("alert").textContent).toBe(copy);
    expect(screen.queryAllByRole("button").map(button => button.textContent)).toEqual(actions);
    expect(screen.queryAllByRole("link").map(link => [link.textContent, link.getAttribute("href")])).toEqual(links);
    expect(screen.getByRole("alert")).not.toHaveTextContent(/private error|future_provider_error/);
  });

  it.each([...MODEL_FAILURE_CODES])("hides internal information for %s", (code) => {
    renderFailure({ status: "failed", reason: "model_failure", code,
      diagnostic_id: "diag_private", partial_output: false });
    const alert = screen.getByRole("alert");
    expect(alert).not.toHaveTextContent(code);
    expect(alert).not.toHaveTextContent("diag_private");
    expect(alert).not.toHaveTextContent(/[A-Za-z_]/);
    expect(alert).toHaveTextContent("本次未生成可用内容。");
  });

  it.each([
    ["authentication_failed", "模型身份验证失败", "密钥是否有效"],
    ["permission_denied", "模型权限不足", "无权使用"],
    ["request_timeout", "模型响应超时", "重试或切换模型"],
    ["rate_limited", "请求频率超限", "稍后重试"],
    ["quota_exhausted", "调用额度不足", "供应商账户"],
    ["not_found", "模型或服务入口不可用", "检查模型配置"],
    ["token_limit", "模型调用达到长度限制", "减少内容"],
  ])("gives an accurate explanation for %s", (code, title, guidance) => {
    renderFailure({ status: "failed", reason: "model_failure", code, partial_output: false });
    expect(screen.getByRole("alert")).toHaveTextContent(title);
    expect(screen.getByRole("alert")).toHaveTextContent(guidance);
    if (code === "permission_denied") {
      expect(screen.getByRole("alert")).not.toHaveTextContent(/密钥|身份验证失败/);
    }
  });

  it("offers safe fallback and recovery for the reported upstream failure", () => {
    const retry = vi.fn();
    const switchModel = vi.fn();
    window.addEventListener(CHAT_OPEN_MODEL_SELECTOR_EVENT, switchModel);
    try {
      renderFailure({ status: "failed", reason: "runtime_failure",
        code: "upstream_request_failed", diagnostic_id: "diag_reported", partial_output: false }, retry);
      expect(screen.getByRole("alert")).toHaveTextContent("暂时无法完成本次回复，请稍后重试或切换模型。");
      expect(screen.getByRole("alert")).not.toHaveTextContent(/upstream_request_failed|diag_reported|密钥|额度不足/);
      fireEvent.click(screen.getByRole("button", { name: "再试一次" }));
      fireEvent.click(screen.getByRole("button", { name: "切换模型" }));
      expect(retry).toHaveBeenCalledOnce();
      expect((switchModel.mock.calls[0][0] as CustomEvent).detail).toEqual({ conversationId: "test-chat" });
    } finally {
      window.removeEventListener(CHAT_OPEN_MODEL_SELECTOR_EVENT, switchModel);
    }
  });

  it("does not suggest a model change for an explicit host failure", () => {
    renderFailure({ status: "failed", reason: "runtime_failure",
      code: "upstream_request_failed", model_invoked: false, partial_output: false });
    expect(screen.queryByRole("button", { name: "切换模型" })).not.toBeInTheDocument();
    expect(screen.getByRole("alert")).not.toHaveTextContent("切换模型");
  });

  it.each([
    ["protocol_error", "模型返回的内容暂时无法处理"],
    ["transport_error", "无法稳定连接到模型服务"],
  ])("distinguishes runtime and model failures with the same code %s", (code, modelMessage) => {
    const terminal: RunTerminalView = { status: "failed", reason: "runtime_failure",
      code, diagnostic_id: "diag_runtime", partial_output: true };
    const { rerender } = renderFailure(terminal);
    expect(screen.getByRole("alert")).toHaveTextContent("暂时无法完成本次回复，请稍后重试。");
    expect(screen.getByRole("alert")).toHaveTextContent("已生成的部分内容已保留。");
    expect(screen.getByRole("alert")).not.toHaveTextContent(modelMessage);
    expect(screen.getByRole("alert")).not.toHaveTextContent(/切换模型|diag_runtime/);
    expect(screen.queryByRole("button", { name: "切换模型" })).not.toBeInTheDocument();
    expect(screen.queryByRole("link", { name: "检查配置" })).not.toBeInTheDocument();

    rerender(<I18nextProvider i18n={i18n}>
      <RunStatusCard terminal={{ ...terminal, reason: "model_failure" }} conversationId="test-chat" />
    </I18nextProvider>);
    expect(screen.getByRole("alert")).toHaveTextContent(modelMessage);
    expect(screen.getByRole("button", { name: "切换模型" })).toBeInTheDocument();
  });

  it("uses a generic fallback for an unknown provider code", () => {
    renderFailure({ status: "failed", reason: "model_failure",
      code: "secret_provider_code", diagnostic_id: "diag_unknown", partial_output: true });
    expect(screen.getByRole("alert")).toHaveTextContent("暂时无法完成本次回复，请稍后重试或切换模型。");
    expect(screen.getByRole("alert")).toHaveTextContent("已生成的部分内容已保留。");
    expect(screen.getByRole("alert")).not.toHaveTextContent(/secret_provider_code|diag_unknown/);
  });

  it("preserves cancellation without showing stale failure details or recovery actions", () => {
    renderFailure({ status: "cancelled", reason: "user_cancelled", code: "authentication_failed",
      diagnostic_id: "diag_cancelled", partial_output: true });
    expect(screen.getByRole("alert")).toHaveTextContent("已取消生成");
    expect(screen.getByRole("alert")).not.toHaveTextContent(/身份验证失败|diag_cancelled/);
    expect(screen.queryByRole("button")).not.toBeInTheDocument();
    expect(screen.queryByRole("link")).not.toBeInTheDocument();
  });

  it("keeps a disabled retry from starting another request", () => {
    const retry = vi.fn();
    render(<I18nextProvider i18n={i18n}>
      <RunStatusCard terminal={{ status: "failed", reason: "model_failure",
        code: "request_timeout", partial_output: false }} onRetry={retry} retryDisabled />
    </I18nextProvider>);
    const button = screen.getByRole("button", { name: "再试一次" });
    expect(button).toBeDisabled();
    fireEvent.click(button);
    expect(retry).not.toHaveBeenCalled();
  });

  it("classifies missing configuration before SSE without claiming an invalid key", () => {
    const mapped = parseCoreChatStreamError({ code: 2001312, message: "api_key is required" }, 400)!;
    const messages = applyChatStreamFailure([{ role: "user", delta: "test question" }],
      "assistant", mapped.terminal);
    renderFailure(messages[1].run_terminal);
    expect(screen.getByRole("alert")).toHaveTextContent("模型配置暂时不可用");
    expect(screen.getByRole("alert")).not.toHaveTextContent(/身份验证失败|密钥失效/);
    expect(screen.getByRole("link", { name: "检查配置" })).toHaveAttribute("href",
      "/settings?section=models&view=providers&return_to=%2Fagent%2Fchat%2Fhome%2Ftest-chat");
    expect(screen.getByRole("button", { name: "检查后重试" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "切换模型" })).toBeInTheDocument();
  });

  it("renders restored history without exposing stored diagnostics", () => {
    const terminal = { status: RunTerminalStatusEnum.Failed, reason: RunTerminalReasonEnum.ModelFailure, code: "request_timeout",
      diagnostic_id: "diag_history", partial_output: true };
    const messages = buildChatMessageListFromHistory([{ id: "history-test", query: "test question",
      result: "partial answer", run_status: "failed", run_terminal: terminal }]);
    renderFailure(messages[1].run_terminal);
    expect(screen.getByRole("alert")).toHaveTextContent("模型响应超时");
    expect(screen.getByRole("alert")).toHaveTextContent("已生成的部分内容已保留。");
    expect(screen.getByRole("alert")).not.toHaveTextContent(/request_timeout|diag_history/);
  });

  it("keeps configuration failure copy and recovery actions after restoring history", () => {
    const mapped = parseCoreChatStreamError({ code: 2001597, message: "model config unavailable" }, 503)!;
    const live = applyChatStreamFailure([{ role: "user", delta: "test question" }],
      "assistant", mapped.terminal);
    const retry = vi.fn();
    const { rerender } = renderFailure(live[1].run_terminal, retry);
    const immediateCopy = screen.getByRole("alert").textContent;
    expect(screen.getByRole("alert")).toHaveTextContent("模型配置暂时不可用");

    const restored = buildChatMessageListFromHistory([{ id: "configuration-history", query: "test question",
      run_status: "failed", run_terminal: {
        status: RunTerminalStatusEnum.Failed, reason: RunTerminalReasonEnum.ModelFailure,
        code: "model_configuration", partial_output: false, diagnostic_id: "diag_configuration",
      } }]);
    rerender(<I18nextProvider i18n={i18n}>
      <RunStatusCard terminal={restored[1].run_terminal} conversationId="test-chat" onRetry={retry} />
    </I18nextProvider>);
    expect(screen.getByRole("alert").textContent).toBe(immediateCopy);
    expect(screen.getByRole("alert")).not.toHaveTextContent(/model_configuration|diag_configuration/);
    expect(screen.getByRole("link", { name: "检查配置" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "切换模型" })).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "检查后重试" }));
    expect(retry).toHaveBeenCalledOnce();
  });
});
