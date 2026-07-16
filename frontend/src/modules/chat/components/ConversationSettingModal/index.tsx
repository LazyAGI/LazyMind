import { CommonModal } from "@/components/ui";
import { localizeErrorCode } from "@/components/request";
import { Switch, Space, message } from "antd";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { useConversationSettings } from "@/modules/chat/store/conversationSettings";
import { ChatServiceApi } from "@/modules/chat/utils/request";
import { useChatNewMessageStore } from "@/modules/chat/store/chatNewMessage";

interface ConversationSettingModalProps {
  cancelFn: () => void;
  initialStatus?: number;
  onStatusChange?: () => void;
}

function ConversationSettingModal(props: ConversationSettingModalProps) {
  const { t } = useTranslation();
  const { cancelFn, initialStatus, onStatusChange } = props;
  const { enableMultipleAnswers, setEnableMultipleAnswers } =
    useConversationSettings();
  const { newMessage } = useChatNewMessageStore();
  const [localEnableMultipleAnswers, setLocalEnableMultipleAnswers] = useState(
    enableMultipleAnswers,
  );

  useEffect(() => {
    if (initialStatus !== undefined) {
      const newValue = initialStatus === 1;
      setEnableMultipleAnswers(newValue);
      setLocalEnableMultipleAnswers(newValue);
    }
  }, [initialStatus, setEnableMultipleAnswers]);

  async function successFn() {
    try {
      const status = localEnableMultipleAnswers ? 1 : 0;

      const response =
        await ChatServiceApi().conversationServiceSetMultiAnswersSwitchStatus({
          setMultiAnswersSwitchStatusRequest: {
            status,
          },
        });

      const savedStatus = response.data.status ?? status;
      setEnableMultipleAnswers(savedStatus === 1);
      if (!newMessage && savedStatus === 0) {
        message.success(t("chat.keepLazyMindAnswer"));
      } else {
        message.success(t("chat.settingsSaved"));
      }
      onStatusChange?.();
      cancelFn();
    } catch (error: any) {
      if (!error?.response && !error?.request) {
        message.error(localizeErrorCode("2000509"));
      }
    }
  }

  function renderContent() {
    return (
      <div>
        <Space direction="vertical" size={12} style={{ width: "100%" }}>
          <Space>
            <span>{t("chat.enableTwoAnswers")}</span>
            <Switch
              checked={localEnableMultipleAnswers}
              onChange={(val) => {
                setLocalEnableMultipleAnswers(val);
              }}
            />
          </Space>
          <div style={{ fontSize: 12, color: "#8d9ab2" }}>
            {t("chat.enableTwoAnswersDesc")}
          </div>
        </Space>
      </div>
    );
  }

  return (
    <CommonModal
      contentText={renderContent()}
      title={t("chat.conversationSettings")}
      cancelFn={cancelFn}
      successFn={successFn}
    />
  );
}

export default ConversationSettingModal;
