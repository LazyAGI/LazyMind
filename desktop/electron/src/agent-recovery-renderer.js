const messages = {
  zh: {
    title: "重新连接 Codex", description: "请先保存当前工作。重启后将返回原会话，接续刚才的请求。",
    cancel: "暂不重启", confirm: "重启并连接", close: "关闭", failed: "连接尚未确认", details: "技术详情", genericError: "暂时无法确认连接状态，请稍后重试。",
    checking: "正在检查连接…", closing: "正在关闭 Codex…", starting: "正在启动 Codex…", connecting: "正在连接 Codex…",
    restoring: "正在打开原 Codex 会话…", resuming: "正在接续刚才的请求…",
    'waiting-chat': "正在等待原会话恢复就绪…", 'waiting-idle': "原会话正在处理其他任务，等待结束…", 'showing-panel': "正在恢复结果面板…",
    connected: "恢复请求已送达", success: "已返回原会话，无需再次输入“继续”。流程仍按原有审核规则执行。", problem: "Codex 连接不可用",
    CODEX_CDP_UNAVAILABLE: "暂未确认 Codex 连接。若应用已启动，请稍等片刻；系统会自动检查。",
    CODEX_CONTROL_UNAVAILABLE: "Codex 的会话控制接口尚未就绪或不兼容。请等待加载完成后重试。",
    CODEX_CONFIGURATION_UNAVAILABLE: "Codex 或已保存的接入配置不可用，请检查安装与接入配置。",
    closeFailed: "Codex 未能正常退出。请处理 Codex 中尚未关闭的对话框后重试。",
    expired: "本次恢复请求已失效。请回到原聊天重新执行刚才的操作。",
    timeout: "等待 Codex 恢复连接超时，请确认 Codex 已完成启动后重试。",
    uncertain: "暂时无法确认恢复请求是否送达，未重复发送。请稍后重试以核对送达状态。",
    connectedOnly: "连接已恢复", successOnly: "Codex 的会话控制连接已恢复。",
    viewed: "结果面板已恢复", viewSuccess: "已返回原会话，可以查看已有结果。流程状态保持原样。",
    viewDescription: "请先保存当前工作。重启后将返回原会话并恢复结果面板，工作流保持当前状态。",
  },
  en: {
    title: "Restart Codex to continue", description: "Session control is unavailable. Restart to return to the original chat and pick up your request automatically. Save your current work first.",
    cancel: "Not now", confirm: "Restart and connect", close: "Close", failed: "Connection not yet confirmed", details: "Technical details", genericError: "Unable to confirm the connection. Try again shortly.",
    checking: "Checking connection…", closing: "Closing Codex…", starting: "Starting Codex…", connecting: "Connecting to Codex…",
    restoring: "Opening the original Codex chat…", resuming: "Picking up your request…",
    'waiting-chat': "Waiting for the original chat to finish restoring…", 'waiting-idle': "Waiting for the current task in the original chat…", 'showing-panel': "Restoring the results panel…",
    connected: "Recovery request delivered", success: "Returned to the original chat. No need to type Continue again. Normal Workflow review rules still apply.", problem: "Codex connection unavailable",
    CODEX_CDP_UNAVAILABLE: "Cannot verify Codex's connection port. Check whether another application is using it.",
    CODEX_CONTROL_UNAVAILABLE: "Codex's session-control interfaces are not ready or compatible. Wait for loading to finish and retry.",
    CODEX_CONFIGURATION_UNAVAILABLE: "Codex or its saved connection configuration is unavailable. Check the installation and connection settings.",
    closeFailed: "Codex could not close normally. Finish any pending dialogs in Codex and retry.",
    expired: "This recovery request has expired. Retry your original operation in the original chat.",
    timeout: "Timed out waiting for Codex to connect. Check that Codex has finished starting and retry.",
    uncertain: "Recovery delivery is uncertain. No duplicate was sent. Retry shortly to check its delivery status.",
    connectedOnly: "Connection restored", successOnly: "Codex session control is connected.",
    viewed: "Results panel restored", viewSuccess: "Returned to the original chat. Existing results are available; Workflow state is unchanged.",
    viewDescription: "Reconnect to view existing results. Restart will return to the original chat and restore its panel. Save your current work first.",
  },
};
function render(state) {
  if (!state) return;
  const text = messages[state.language] || messages.en;
  document.documentElement.lang = state.language;
  const success = state.stage === "connected";
  document.getElementById("title").textContent = success ? state.view_only ? text.viewed : state.has_task ? text.connected : text.connectedOnly : state.stage === "failed" ? text.failed : state.can_restart ? text.title : text.problem;
  document.getElementById("description").textContent = success ? state.view_only ? text.viewSuccess : state.has_task ? text.success : text.successOnly : state.busy ? text[state.stage] : state.can_restart ? state.view_only ? text.viewDescription : text.description : text[state.code];
  const error = document.getElementById("error");
  document.getElementById("failure").hidden = !state.error;
  document.getElementById("description").hidden = Boolean(state.error);
  document.getElementById("details-label").textContent = text.details;
  document.getElementById("error-detail").textContent = state.error || "";
  error.textContent = /Codex (refused to close|did not (close|exit)|could not close normally)/i.test(state.error) ? text.closeFailed
    : /recovery request expired/i.test(state.error) ? text.expired
    : /delivery.*uncertain|receipt.*uncertain/i.test(state.error) ? text.uncertain
    : /timed out|connection did not become ready|TimeoutError/i.test(state.error) ? text.timeout
    : /CODEX_CDP_UNAVAILABLE/i.test(state.error) ? text.CODEX_CDP_UNAVAILABLE
    : /CODEX_CONTROL_UNAVAILABLE/i.test(state.error) ? text.CODEX_CONTROL_UNAVAILABLE : text.genericError;
  const confirm = document.getElementById("confirm");
  confirm.textContent = text.confirm;
  confirm.hidden = !state.can_restart || success;
  confirm.disabled = state.busy;
  const cancel = document.getElementById("cancel");
  cancel.textContent = state.can_restart && !success ? text.cancel : text.close;
  cancel.disabled = state.busy;
}
window.agentRecovery.onState(render);
window.agentRecovery.state().then(render);
document.getElementById("confirm").addEventListener("click", () => { void window.agentRecovery.confirm(); });
document.getElementById("cancel").addEventListener("click", () => { void window.agentRecovery.dismiss(); });
