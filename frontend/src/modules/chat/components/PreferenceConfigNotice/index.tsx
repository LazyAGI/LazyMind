import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router-dom";
import { Button } from "antd";
import {
  fetchUserUiPreferences,
  patchUserUiPreferences,
} from "@/modules/user/uiPreferencesApi";

interface Props {
  /** 如果为 true，则不渲染（避免和模型未配置警告同时出现） */
  hidden?: boolean;
}

const PreferenceConfigNotice = ({ hidden }: Props) => {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [visible, setVisible] = useState(false);

  useEffect(() => {
    if (hidden) return;
    fetchUserUiPreferences()
      .then((prefs) => {
        if (prefs.chat_preference_notice_dismissed) return;
        if (prefs.user_preference_configured) return;
        setVisible(true);
      })
      .catch((error) => {
        console.error("Failed to load UI preferences:", error);
      });
  }, [hidden]);

  if (!visible) return null;

  const handleDismiss = () => {
    setVisible(false);
    patchUserUiPreferences({ chat_preference_notice_dismissed: true }).catch(
      (error) => {
        console.error("Failed to persist preference notice dismissal:", error);
      },
    );
  };

  return (
    <div
      className="model-provider-warning-banner preference-config-notice"
      role="alert"
    >
      <span className="model-provider-warning-text">
        {t("chat.preferenceNotConfigured")}
      </span>
      <Button
        type="primary"
        size="small"
        className="model-provider-warning-action"
        onClick={() => navigate("/memory-management/experience")}
      >
        {t("chat.goToConfigure")}
      </Button>
      <Button
        type="link"
        size="small"
        className="preference-config-dismiss"
        onClick={handleDismiss}
      >
        {t("chat.dontShowAgain")}
      </Button>
    </div>
  );
};

export default PreferenceConfigNotice;
