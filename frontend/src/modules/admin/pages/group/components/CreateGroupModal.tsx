import { Modal, Form, Input, message } from "antd";
import { useState, useEffect } from "react";
import { useTranslation } from "react-i18next";
import { getLocalizedErrorMessage } from "@/components/request";
import { createGroupApi } from "@/modules/signin/utils/request";
import type { GroupItem } from "@/api/generated/auth-client";

const GROUP_NAME_MAX_LENGTH = 100;
const GROUP_REMARK_MAX_LENGTH = 200;

interface CreateGroupModalProps {
  visible: boolean;
  editingGroup?: GroupItem | null;
  onCancel: () => void;
  onSuccess: () => void;
}

const CreateGroupModal = ({
  visible,
  editingGroup,
  onCancel,
  onSuccess,
}: CreateGroupModalProps) => {
  const { t } = useTranslation();
  const [form] = Form.useForm();
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    if (visible) {
      if (editingGroup) {
        form.setFieldsValue({
          group_name: editingGroup.group_name,
          remark: editingGroup.remark,
          tenant_id: editingGroup.tenant_id,
        });
      } else {
        form.resetFields();
      }
    }
  }, [visible, editingGroup, form]);

  const onFinish = async (values: any) => {
    setLoading(true);
    try {
      const groupApi = createGroupApi();
      if (editingGroup) {
        await groupApi.updateGroupApiAuthserviceGroupGroupIdPatch({
          groupId: editingGroup.group_id,
          groupUpdateBody: {
            group_name: values.group_name,
            remark: values.remark,
            tenant_id: values.tenant_id,
          },
        });
        message.success(t("admin.updateGroupSuccess"));
      } else {
        await groupApi.createGroupApiAuthserviceGroupPost({
          groupCreateBody: {
            group_name: values.group_name,
            remark: values.remark,
            tenant_id: values.tenant_id,
          },
        });
        message.success(t("admin.createGroupSuccess"));
      }
      onSuccess();
    } catch (error: any) {
      console.error("Operation failed:", error);
      if (!error?.response && !error?.request) {
        message.error(getLocalizedErrorMessage(error));
      }
    } finally {
      setLoading(false);
    }
  };

  return (
    <Modal
      title={editingGroup ? t("admin.editGroup") : t("admin.newGroup")}
      open={visible}
      onCancel={onCancel}
      onOk={() => form.submit()}
      confirmLoading={loading}
      destroyOnHidden
    >
      <Form form={form} layout="vertical" onFinish={onFinish}>
        <Form.Item
          name="group_name"
          label={t("admin.groupName")}
          rules={[
            { required: true, message: t("admin.enterGroupName") },
            {
              max: GROUP_NAME_MAX_LENGTH,
              message: t("admin.groupNameMax", { max: GROUP_NAME_MAX_LENGTH }),
            },
          ]}
        >
          <Input
            placeholder={t("admin.enterGroupNameWithMax", { max: GROUP_NAME_MAX_LENGTH })}
            maxLength={GROUP_NAME_MAX_LENGTH}
            showCount
          />
        </Form.Item>

        <Form.Item
          name="remark"
          label={t("admin.description")}
          rules={[
            {
              max: GROUP_REMARK_MAX_LENGTH,
              message: t("admin.descriptionMax", { max: GROUP_REMARK_MAX_LENGTH }),
            },
          ]}
        >
          <Input.TextArea
            placeholder={t("admin.enterDescriptionWithMax", { max: GROUP_REMARK_MAX_LENGTH })}
            maxLength={GROUP_REMARK_MAX_LENGTH}
            showCount
            autoSize={{ minRows: 3, maxRows: 6 }}
          />
        </Form.Item>
      </Form>
    </Modal>
  );
};

export default CreateGroupModal;
