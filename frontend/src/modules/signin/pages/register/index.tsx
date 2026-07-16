import { useState } from "react";
import { Button, Form, Input, message } from "antd";
import { useNavigate } from "react-router-dom";
import { registerByPassword } from "@/modules/signin/utils/request";
import {
  passwordRules,
  USERNAME_MAX_LENGTH,
  usernameRules,
} from "@/modules/signin/utils/formRules";
import { useTranslation } from "react-i18next";
import { localizeErrorCode } from "@/components/request";

interface RegisterFormValues {
  username: string;
  email?: string;
  password: string;
  confirmPassword: string;
}

const Register = () => {
  const [form] = Form.useForm();
  const navigate = useNavigate();
  const [loading, setLoading] = useState(false);
  const { t } = useTranslation();

  const onFinish = async (values: RegisterFormValues) => {
    setLoading(true);
    try {
      await registerByPassword({
        username: values.username,
        password: values.password,
        confirm_password: values.confirmPassword,
        email: values.email || undefined,
      });
      message.success(t("auth.registerSuccess"));
      navigate("/login", { state: { username: values.username } });
    } catch (error: any) {
      if (!error?.response && !error?.request) {
        message.error(localizeErrorCode("2000509"));
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
          {t("auth.newUserRegister")}
        </h2>
      </div>
      <Form
        form={form}
        layout="vertical"
        onFinish={onFinish}
        autoComplete="off"
        requiredMark={false}
        style={{ marginBottom: 0 }}
      >
        <Form.Item
          name="username"
          label={t("auth.username")}
          rules={usernameRules}
        >
          <Input
            placeholder={t("auth.pleaseInputUsername")}
            autoComplete="username"
            maxLength={USERNAME_MAX_LENGTH}
            showCount
          />
        </Form.Item>

        <Form.Item
          name="email"
          label={t("auth.email")}
          rules={[
            { type: "email", message: t("auth.invalidEmail") },
          ]}
        >
          <Input placeholder={t("auth.pleaseInputEmail")} autoComplete="email" />
        </Form.Item>

        <Form.Item
          name="password"
          label={t("auth.setPassword")}
          rules={passwordRules}
        >
          <Input.Password
            placeholder={t("auth.pleaseInputPasswordSet")}
            autoComplete="new-password"
          />
        </Form.Item>

        <Form.Item
          name="confirmPassword"
          label={t("auth.confirmPassword")}
          dependencies={['password']}
          rules={[
            { required: true, message: t("auth.pleaseInputConfirmPassword") },
            ({ getFieldValue }) => ({
              validator(_, value) {
                if (!value || getFieldValue('password') === value) {
                  return Promise.resolve();
                }
                return Promise.reject(new Error(t("auth.passwordNotMatch")));
              },
            }),
          ]}
        >
          <Input.Password
            placeholder={t("auth.pleaseInputConfirmPassword")}
            autoComplete="new-password"
          />
        </Form.Item>

        <Form.Item style={{ marginTop: '16px', marginBottom: 0 }}>
          <Button type="primary" htmlType="submit" block loading={loading}>
            {t("auth.register")}
          </Button>
          <div style={{ textAlign: 'center', marginTop: '12px', color: '#86909c', fontSize: '13px' }}>
            {t("auth.hasAccount")} <a style={{ color: '#1677ff', fontWeight: 500 }} onClick={() => navigate("/login")}>{t("auth.backToLogin")}</a>
          </div>
        </Form.Item>
      </Form>
    </div>
  );
};

export default Register;
