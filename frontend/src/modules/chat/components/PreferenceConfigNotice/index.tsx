import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router-dom";
import { Button } from "antd";
import {
  fetchUserUiPreferences,
  patchUserUiPreferences,
} from "@/modules/user/uiPreferencesApi";

interface Props {
  /** 如果为 true，则配置服务尚不可用，暂不渲染 */
  hidden?: boolean;
}

const PreferenceConfigNotice = ({ hidden }: Props) => {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [visible, setVisible] = useState(false);

  useEffect(() => {
    let cancelled = false;
    let retryTimer: number | undefined;

    const updateVisibility = (nextVisible: boolean) => {
      if (cancelled) return;
      setVisible(nextVisible);
    };

    if (hidden) {
      updateVisibility(false);
      return () => {
        cancelled = true;
      };
    }

    const loadPreferences = () => {
      fetchUserUiPreferences({ silentError: true } as never)
        .then((prefs) => {
          updateVisibility(
            !prefs.chat_preference_notice_dismissed &&
              !prefs.user_preference_configured,
          );
        })
        .catch(() => {
          if (!cancelled) {
            retryTimer = window.setTimeout(loadPreferences, 1000);
          }
        });
    };

    loadPreferences();
    return () => {
      cancelled = true;
      if (retryTimer !== undefined) {
        window.clearTimeout(retryTimer);
      }
    };
  }, [hidden]);

  if (hidden || !visible) return null;

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
