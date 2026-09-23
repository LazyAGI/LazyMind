import { useEffect, useRef, useState } from "react";
import { Button, Card, Result, Spin } from "antd";
import { useTranslation } from "react-i18next";
import { authorizeMcpServer } from "@/modules/memory/toolApi";

// A same-origin tab owns its own OAuth state, so concurrent chat connections
// cannot overwrite each other's server identity or return destination.
export default function McpOAuthConnect() {
  const { t } = useTranslation();
  const started = useRef(false);
  const [failed, setFailed] = useState(false);
  const connect = async () => {
    setFailed(false);
    const params = new URLSearchParams(window.location.search);
    const server = params.get("server");
    const conversation = params.get("conversation");
    if (!server || !conversation) { setFailed(true); return; }
    try { await authorizeMcpServer(server, conversation); }
    catch { setFailed(true); }
  };
  useEffect(() => {
    if (started.current) return;
    started.current = true;
    void connect();
  }, []);
  return <main style={{ minHeight: "100vh", display: "grid", placeItems: "center", padding: 24 }}>
    <Card>{failed
      ? <Result status="error" title={t("admin.memoryMcpOAuthError")}
          extra={<Button onClick={() => void connect()}>{t("toolConfiguration.retryConnection")}</Button>} />
      : <><Spin /><p>{t("toolConfiguration.connecting")}</p></>}
    </Card>
  </main>;
}
