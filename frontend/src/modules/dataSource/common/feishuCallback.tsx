import { Button, Result, Spin, Typography } from "antd";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { useSearchParams } from "react-router-dom";

import {
  FEISHU_DATA_SOURCE_OAUTH_CHANNEL,
  finishCloudDataSourceOAuth,
  getDataSourceManagementUrl,
  getCloudDataSourceOAuthReturnUrl,
  saveCloudDataSourceOAuthResult,
  type CloudDataSourceProvider,
  type FeishuDataSourceOAuthMessage,
} from "./feishuOAuth";

const { Paragraph } = Typography;

type CallbackStatus = "loading" | "success" | "error";

interface CallbackViewState {
  status: CallbackStatus;
  title: string;
  subtitle?: string;
  returnUrl?: string;
}

function isPopupWindow() {
  return Boolean(window.opener && !window.opener.closed);
}

const CALLBACK_REDIRECT_DELAY_MS = 100;
const POPUP_CLOSE_FALLBACK_DELAY_MS = 150;

interface DataSourceOAuthCallbackProps {
  provider?: CloudDataSourceProvider;
}

export default function FeishuDataSourceCallback({
  provider = "feishu",
}: DataSourceOAuthCallbackProps) {
  const { t } = useTranslation();
  const [searchParams] = useSearchParams();
  const [viewState, setViewState] = useState<CallbackViewState>({
    status: "loading",
    title: t("admin.dataSourceCallbackLoadingTitle"),
    subtitle: t("admin.dataSourceCallbackLoadingSubtitle"),
  });

  useEffect(() => {
    const returnToDataSourcePage = (returnUrl: string) => {
      window.setTimeout(() => {
        if (isPopupWindow()) {
          window.close();

          window.setTimeout(() => {
            if (!window.closed) {
              window.location.replace(returnUrl);
            }
          }, POPUP_CLOSE_FALLBACK_DELAY_MS);
          return;
        }

        window.location.replace(returnUrl);
      }, CALLBACK_REDIRECT_DELAY_MS);
    };

    const finalize = (payload: FeishuDataSourceOAuthMessage) => {
      saveCloudDataSourceOAuthResult(provider, payload);

      if (isPopupWindow()) {
        window.opener?.postMessage(payload, window.location.origin);
      }
    };

    const run = async () => {
      const code = searchParams.get("code");
      const state = searchParams.get("state");
      const error = searchParams.get("error");
      const errorDescription = searchParams.get("error_description");

      if (error) {
        const message =
          errorDescription ||
          t("admin.dataSourceCallbackErrorWithCode", { code: error });
        setViewState({
          status: "error",
          title: t("admin.dataSourceCallbackErrorTitle"),
          subtitle: message,
        });
        finalize({
          channel: FEISHU_DATA_SOURCE_OAUTH_CHANNEL,
          source: `${provider}-data-source` as FeishuDataSourceOAuthMessage["source"],
          status: "error",
          message,
          provider,
        });
        return;
      }

      if (!code || !state) {
        const message = t("admin.dataSourceCallbackMissingParams");
        setViewState({
          status: "error",
          title: t("admin.dataSourceCallbackErrorTitle"),
          subtitle: message,
        });
        finalize({
          channel: FEISHU_DATA_SOURCE_OAUTH_CHANNEL,
          source: `${provider}-data-source` as FeishuDataSourceOAuthMessage["source"],
          status: "error",
          message,
          provider,
        });
        return;
      }

      try {
        const returnUrl = getCloudDataSourceOAuthReturnUrl(provider, state);
        const connection = await finishCloudDataSourceOAuth(provider, code, state);
        setViewState({
          status: "success",
          title: t("admin.dataSourceCallbackSuccessTitle"),
          subtitle: t("admin.dataSourceCallbackSuccessSubtitle", {
            accountName: connection.accountName,
          }),
          returnUrl,
        });
        finalize({
          channel: FEISHU_DATA_SOURCE_OAUTH_CHANNEL,
          source: `${provider}-data-source` as FeishuDataSourceOAuthMessage["source"],
          status: "success",
          connection,
        });

        returnToDataSourcePage(returnUrl);
      } catch (error: any) {
        const message = error?.message || t("admin.dataSourceOauthFailedRetry");
        setViewState({
          status: "error",
          title: t("admin.dataSourceCallbackErrorTitle"),
          subtitle: message,
        });
        finalize({
          channel: FEISHU_DATA_SOURCE_OAUTH_CHANNEL,
          source: `${provider}-data-source` as FeishuDataSourceOAuthMessage["source"],
          status: "error",
          message,
          provider,
        });
      }
    };

    void run();
  }, [provider, searchParams, t]);

  return (
    <div
      style={{
        minHeight: "100vh",
        display: "flex",
        alignItems: "center",
        justifyContent: "center",
        padding: "24px",
        background:
          "radial-gradient(circle at top, rgba(22,119,255,0.08), transparent 42%), #f8fafc",
      }}
    >
      <div
        style={{
          width: "min(520px, 100%)",
          borderRadius: 20,
          border: "1px solid #e5e7eb",
          background: "#fff",
          boxShadow: "0 24px 48px rgba(15, 23, 42, 0.08)",
          padding: 28,
        }}
      >
        {viewState.status === "loading" ? (
          <div
            style={{
              minHeight: 220,
              display: "flex",
              flexDirection: "column",
              alignItems: "center",
              justifyContent: "center",
              gap: 16,
            }}
          >
            <Spin size="large" />
            <div style={{ textAlign: "center" }}>
              <div style={{ fontSize: 20, fontWeight: 600, color: "#111827" }}>
                {viewState.title}
              </div>
              <Paragraph style={{ marginTop: 8, marginBottom: 0, color: "#6b7280" }}>
                {viewState.subtitle}
              </Paragraph>
            </div>
          </div>
        ) : (
          <Result
            status={viewState.status}
            title={viewState.title}
            subTitle={viewState.subtitle}
            extra={
              <Button
                type="primary"
                onClick={() =>
                  window.location.replace(
                    viewState.status === "success" && viewState.returnUrl
                      ? viewState.returnUrl
                      : getDataSourceManagementUrl(provider),
                  )
                }
              >
                {t("admin.dataSourceCallbackBack")}
              </Button>
            }
          />
        )}
      </div>
    </div>
  );
}
