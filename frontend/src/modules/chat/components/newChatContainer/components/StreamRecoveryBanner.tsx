import {
  CheckCircleOutlined,
  DisconnectOutlined,
  ReloadOutlined,
} from "@ant-design/icons";
import { Alert, Button } from "antd";
import { useTranslation } from "react-i18next";
import type { StreamRecoveryViewState } from "@/modules/chat/utils/streamRecovery";

interface StreamRecoveryBannerProps {
  recovery: StreamRecoveryViewState;
  onReconnect: () => void;
}

export default function StreamRecoveryBanner({
  recovery,
  onReconnect,
}: StreamRecoveryBannerProps) {
  const { t } = useTranslation();

  if (recovery.status === "idle") {
    return null;
  }

  const failed = recovery.status === "failed";
  const recovered = recovery.status === "recovered";
  return (
    <div
      className={`chat-stream-recovery-banner is-${recovery.status}`}
      aria-live={failed ? "assertive" : "polite"}
    >
      <Alert
        role={failed ? "alert" : "status"}
        type={failed ? "error" : recovered ? "success" : "warning"}
        showIcon
        icon={
          failed ? (
            <DisconnectOutlined />
          ) : recovered ? (
            <CheckCircleOutlined />
          ) : (
            <ReloadOutlined spin />
          )
        }
        message={
          failed
            ? t("chat.streamResumeFailed")
            : recovered
              ? t("chat.streamRecovered")
              : t("chat.streamResuming", {
                  attempt: recovery.attempt,
                  max: recovery.maxAttempts,
                })
        }
        action={
          failed ? (
            <Button size="small" danger onClick={onReconnect}>
              {t("chat.streamReconnect")}
            </Button>
          ) : undefined
        }
      />
    </div>
  );
}
