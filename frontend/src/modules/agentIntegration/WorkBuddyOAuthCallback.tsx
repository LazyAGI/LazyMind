import { Button, Result, Spin } from "antd";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { useSearchParams } from "react-router-dom";

import { getLocalizedErrorMessage } from "@/components/request";
import {
  finishWorkBuddyAuthorization,
  WORKBUDDY_OAUTH_CHANNEL,
} from "./workbuddyOAuth";

type CallbackState = "loading" | "success" | "error";

export default function WorkBuddyOAuthCallback() {
  const { t } = useTranslation();
  const [params] = useSearchParams();
  const [status, setStatus] = useState<CallbackState>("loading");
  const [detail, setDetail] = useState("");

  useEffect(() => {
    const notify = (nextStatus: "success" | "error", message = "") => {
      window.opener?.postMessage(
        { channel: WORKBUDDY_OAUTH_CHANNEL, status: nextStatus, message },
        window.location.origin,
      );
    };
    const run = async () => {
      const code = params.get("code") || "";
      const state = params.get("state") || "";
      if (!code || !state || params.get("error")) {
        const message = t("agentIntegration.workbuddyAuthorizationFailed");
        setStatus("error");
        setDetail(message);
        notify("error", message);
        return;
      }
      try {
        await finishWorkBuddyAuthorization(code, state);
        setStatus("success");
        notify("success");
        window.setTimeout(() => window.close(), 200);
      } catch (error) {
        const message = getLocalizedErrorMessage(error);
        setStatus("error");
        setDetail(message);
        notify("error", message);
      }
    };
    void run();
  }, [params, t]);

  if (status === "loading") {
    return (
      <div style={{ minHeight: "100vh", display: "grid", placeItems: "center" }}>
        <Spin size="large" tip={t("agentIntegration.workbuddyAuthorizing")} />
      </div>
    );
  }
  return (
    <Result
      status={status}
      title={t(
        status === "success"
          ? "agentIntegration.workbuddyAuthorizationSucceeded"
          : "agentIntegration.workbuddyAuthorizationFailed",
      )}
      subTitle={detail}
      extra={<Button onClick={() => window.close()}>{t("common.close")}</Button>}
    />
  );
}
