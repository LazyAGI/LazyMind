import { Form, Input, Modal } from "antd";
import type { FormInstance } from "antd";
import type { TFunction } from "i18next";
import type { FeishuAccountFormValues } from "../../common/feishuAccounts";
import { FeishuCredentialHintAlertFromForm } from "../../common/FeishuCredentialHintAlert";

export interface FeishuAccountFormModalProps {
  t: TFunction;
  open: boolean;
  isEditing: boolean;
  submitting: boolean;
  form: FormInstance<FeishuAccountFormValues>;
  onCancel: () => void;
  onOk: () => void;
}

export default function FeishuAccountFormModal({
  t,
  open,
  isEditing,
  submitting,
  form,
  onCancel,
  onOk,
}: FeishuAccountFormModalProps) {
  return (
    <Modal
      title={
        isEditing
          ? t("admin.dataSourceFeishuAccountEdit")
          : t("admin.dataSourceFeishuAccountCreate")
      }
      open={open}
      destroyOnHidden
      onCancel={() => {
        if (submitting) {
          return;
        }
        onCancel();
      }}
      onOk={onOk}
      okText={t("admin.dataSourceFeishuAccountSaveAndAuthorize")}
      okButtonProps={{ loading: submitting }}
      cancelButtonProps={{ disabled: submitting }}
      cancelText={t("common.cancel")}
    >
      <Form form={form} layout="vertical">
        <Form.Item label={t("admin.dataSourceFeishuAccountName")} name="name">
          <Input placeholder={t("admin.dataSourceFeishuAccountNamePlaceholder")} />
        </Form.Item>
        <Form.Item
          label={t("admin.dataSourceAppId")}
          name="appId"
          rules={[{ required: true, message: t("admin.dataSourceAppIdRequired") }]}
        >
          <Input placeholder={t("admin.dataSourceAppIdPlaceholder")} />
        </Form.Item>
        <Form.Item
          label={t("admin.dataSourceAppSecret")}
          name="appSecret"
          rules={[{ required: true, message: t("admin.dataSourceAppSecretRequired") }]}
        >
          <Input.Password placeholder={t("admin.dataSourceAppSecretPlaceholder")} />
        </Form.Item>
        <FeishuCredentialHintAlertFromForm form={form} />
      </Form>
    </Modal>
  );
}
