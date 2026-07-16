import { Button, message, Spin } from "antd";
import { useEffect, useState } from "react";
import { useSearchParams } from "react-router-dom";
import {
  BASE_URL,
  getLocalizedErrorMessage,
  localizeErrorCode,
} from "@/components/request";
import { useTranslation } from "react-i18next";
import "./index.scss";

const LoginTransition = () => {
  const [searchParams] = useSearchParams();
  const [loading, setLoading] = useState(true);
  const { t } = useTranslation();

  useEffect(() => {
    callThirdPartyLogin();
  }, []);

  const callThirdPartyLogin = async () => {
    const code = searchParams.get("code");
    if (!code) {
      setLoading(false);
      return;
    }
    try {
      const base = BASE_URL || window.location.origin;
      const res = await fetch(
        `${base}/api/authservice/v1/auth/third_party_login?code=${code}`,
        { credentials: "include" },
      );
      if (res.redirected && res.url) {
        window.location.replace(res.url);
        return;
      }
      const data = await res.json().catch(() => ({}) as Record<string, any>);
      const redirectTo =
        data?.redirect_to ||
        data?.redirect_url ||
        data?.data?.redirect_to ||
        "";
      if (redirectTo) {
        window.location.replace(redirectTo);
        return;
      }
      setLoading(false);
      if (!res.ok) {
        message.error(
          getLocalizedErrorMessage({ response: { data, status: res.status } }) ||
            localizeErrorCode("2000509"),
        );
      }
    } catch {
      message.error(localizeErrorCode("2000509"));
      setLoading(false);
    }
  };

  const retryWithNewChallenge = () => {
    document.cookie =
      "login_challenge=; path=/; expires=Thu, 01 Jan 1970 00:00:00 GMT";
    window.location.replace(
      `${window.location.origin}${window.BASENAME || ""}/login`,
    );
  };

  return (
    <Spin tip={t("auth.loggingInWait")} size="large" spinning={loading}>
      <div
        className="login-transition-page"
        style={{
          background: "linear-gradient(135deg,#f5f7fa 0%,#e4e8ec 100%)",
        }}
      >
        <div className="login-transition-card">
          <div className="card-content">
            <div className="card-header">
              <span
                className="card-logo"
                style={{ fontSize: 24, fontWeight: 700 }}
              >
                LazyMind
              </span>
            </div>
            <Button className="retry-btn" onClick={retryWithNewChallenge}>
              {t("auth.retryLogin")}
            </Button>
          </div>
          <div className="card-footer">
            <span
              className="footer-logo-img"
              style={{ fontSize: 12, color: "#999" }}
            >
              LazyMind
            </span>
          </div>
        </div>
      </div>
    </Spin>
  );
};

export default LoginTransition;
