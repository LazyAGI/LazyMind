import { useEffect, useRef, useState } from "react";
import { Button, Card, Result, Spin } from "antd";
import { useTranslation } from "react-i18next";
import { Link } from "react-router-dom";
import { MCP_OAUTH_SERVER_KEY, finishMcpOAuth, discoverMcpServerTools } from "@/modules/memory/toolApi";

export default function McpOAuthCallback() {
  const { t } = useTranslation();
  const started = useRef(false);
  const [status, setStatus] = useState<"loading" | "success" | "error" | "discovery">("loading");
  useEffect(() => {
    if (started.current) return;
    started.current = true;
    const params = new URLSearchParams(window.location.search);
    const code = params.get("code");
    const state = params.get("state");
    const id = sessionStorage.getItem(MCP_OAUTH_SERVER_KEY);
    sessionStorage.removeItem(MCP_OAUTH_SERVER_KEY);
    // Scrub before a request can trigger login redirection. No code is retained for replay.
    window.history.replaceState(null, "", window.location.pathname);
    if (!id || !code || !state || params.has("error")) { setStatus("error"); return; }
    void finishMcpOAuth(id, code, state).then(async () => {
      try {
        const result = await discoverMcpServerTools(id);
        setStatus(result.success ? "success" : "discovery");
      } catch { setStatus("discovery"); }
    }).catch(() => setStatus("error"));
  }, []);
  if (status === "loading") return <main style={{ minHeight: "100vh", display: "grid", placeItems: "center", background: "var(--ant-color-bg-layout, #f5f5f5)" }}><Card style={{ width: "min(560px, 90vw)", textAlign: "center", padding: 32 }}><Spin size="large" /><p>{t("admin.memoryMcpOAuthStatus_pending")}</p></Card></main>;
  return <main style={{ minHeight: "100vh", display: "grid", placeItems: "center", background: "var(--ant-color-bg-layout, #f5f5f5)", padding: 24 }}><Card style={{ width: "min(640px, 100%)", borderRadius: 16 }}><Result status={status === "success" ? "success" : status === "discovery" ? "warning" : "error"}
    title={t(status === "success" ? "admin.memoryMcpOAuthSuccess" : status === "discovery" ? "admin.memoryMcpOAuthDiscoverError" : "admin.memoryMcpOAuthError")}
    extra={<Link to="/settings?section=mcp"><Button type="primary">{t("admin.memoryMcpOAuthReturn")}</Button></Link>} /></Card></main>;
}
