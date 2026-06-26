import { useEffect, useState } from "react";
import { Button, Form, Input, message } from "antd";
import { useLocation, useNavigate } from "react-router-dom";
import {
  loginByPassword,
  storeLoginSession,
  unwrapLoginResponse,
} from "@/modules/signin/utils/request";
import { AgentAppsAuth } from "@/components/auth";
import { useTranslation } from "react-i18next";
import { runtimeFeatures } from "@/runtime/features";

interface LoginForm {
  username: string;
  password: string;
}

const Login = () => {
  const [form] = Form.useForm<LoginForm>();
  const navigate = useNavigate();
  const location = useLocation();
  const [loading, setLoading] = useState(false);
  const { t } = useTranslation();

  const checkUserLogin = () => {
    try {
      const userInfo = AgentAppsAuth.getUserInfo();
      if (userInfo && userInfo.token) {
        navigate("/agent/chat", { replace: true });
      }
    } catch {
      // ignore
    }
  };

  useEffect(() => {
    document.cookie =
      "access_token=; path=/; expires=Thu, 01 Jan 1970 00:00:00 GMT";
    const postLogoutCleanup =
      sessionStorage.getItem("signin_post_logout_cleanup") ||
      localStorage.getItem("signin_post_logout_cleanup");
    if (postLogoutCleanup === "1") {
      localStorage.clear();
      [
        "signin_username",
        "signin_password",
        "signin_login_count",
        "signin_login_tag",
      ].forEach((key) => {
        sessionStorage.removeItem(key);
      });
      sessionStorage.removeItem("signin_post_logout_cleanup");
    }
    checkUserLogin();
    const prefilledUsername = (location.state as { username?: string } | null)
      ?.username;
    if (prefilledUsername) {
      form?.setFieldValue?.("username", prefilledUsername);
    }
  }, [form, location.state]);

  const clearSigninRetryLocalCache = () => {
    [
      "signin_username",
      "signin_password",
      "signin_login_count",
      "signin_login_tag",
    ].forEach((key) => {
      localStorage.removeItem(key);
      sessionStorage.removeItem(key);
    });
  };

  const onSubmit = async (value: LoginForm) => {
    setLoading(true);
    try {
      const res = await loginByPassword({
        username: value.username,
        password: value.password,
      });
      const loginData = unwrapLoginResponse(res.data as any);
      await storeLoginSession(loginData, value.username);
      clearSigninRetryLocalCache();
      navigate("/agent/chat", { replace: true });
    } catch (error: any) {
      if (!error?.response && !error?.request) {
        message.error(error?.message || t("auth.loginFailed"));
      }
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="signin-container">
      <div style={{ paddingBottom: '4px' }}>
        <h2 style={{ 
          fontSize: '18px', 
          fontWeight: 600, 
          color: '#1d2129', 
          textAlign: 'center',
          marginBottom: '20px'
        }}>
          {t("auth.welcomeLogin")}
        </h2>
      </div>
      <Form
        className="sign-form"
        autoComplete="off"
        layout="vertical"
        form={form}
        onFinish={onSubmit}
        requiredMark={false}
      >
        <Form.Item
          name="username"
          label={t("auth.account")}
          rules={[{ required: true, message: t("auth.pleaseInputAccount") }]}
        >
          <Input 
            placeholder={t("auth.pleaseInputAccount")} 
            size="large"
            autoComplete="off"
          />
        </Form.Item>
        <Form.Item
          name="password"
          label={t("auth.password")}
          rules={[{ required: true, message: t("auth.pleaseInputPassword") }]}
        >
          <Input.Password 
            placeholder={t("auth.pleaseInputPassword")} 
            size="large"
            autoComplete="new-password"
          />
        </Form.Item>
        <Form.Item style={{ marginTop: '24px' }}>
          <Button
            block
            type="primary"
            size="large"
            htmlType="submit"
            loading={loading}
          >
            {t("auth.login")}
          </Button>
          {!runtimeFeatures.hideRegister && (
            <div style={{ textAlign: "center", marginTop: "16px", color: '#86909c' }}>
              {t("auth.noAccount")} <a style={{ color: '#1677ff', fontWeight: 500 }} onClick={() => navigate("/register")}>{t("auth.registerNow")}</a>
            </div>
          )}
        </Form.Item>
      </Form>
    </div>
  );
};

export default Login;
