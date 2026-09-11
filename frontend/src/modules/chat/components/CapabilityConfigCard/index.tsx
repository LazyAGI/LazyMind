import { SettingOutlined } from "@ant-design/icons";
import { Alert, Button, Space } from "antd";
import { useTranslation } from "react-i18next";
import { useLocation, useNavigate } from "react-router-dom";
import type { MediaCapabilityDependencyDetail } from "@/modules/chat/utils/mediaCapabilityDependency";
import { buildCapabilitySettingsUrl } from "@/modules/chat/utils/mediaCapabilityDependency";

import "./index.scss";

interface CapabilityConfigCardProps {
  detail?: MediaCapabilityDependencyDetail | null;
  continueDisabled?: boolean;
  continueLoading?: boolean;
  onContinue: () => void;
}

export default function CapabilityConfigCard({
  detail,
  continueDisabled = false,
  continueLoading = false,
  onContinue,
}: CapabilityConfigCardProps) {
  const { t } = useTranslation();
  const location = useLocation();
  const navigate = useNavigate();
  const returnTo = `${location.pathname}${location.search}`;
  const missing = detail?.missing ?? [];

  if (!detail || missing.length === 0) {
    return null;
  }

  return (
    <Alert
      className="chat-capability-config-card"
      type="warning"
      showIcon
      message={missing.length === 1
        ? t("chat.mediaCapabilityRequiredTitle", { capability: missing[0].label })
        : t("chat.mediaCapabilitiesRequiredTitle")}
      description={(
        <div className="chat-capability-config-card__body">
          <p>{detail.message || t("chat.mediaCapabilitiesRequiredDesc")}</p>
          <div className="chat-capability-config-card__requirements">
            {missing.map((item) => (
              <div className="chat-capability-config-card__requirement" key={item.id}>
                <strong>{item.label}</strong>
                {item.reason ? <span>{item.reason}</span> : null}
                <Button
                  size="small"
                  type="link"
                  icon={<SettingOutlined />}
                  onClick={() => navigate(buildCapabilitySettingsUrl(item, returnTo))}
                >
                  {t("chat.configureThisCapability")}
                </Button>
              </div>
            ))}
          </div>
          <Space className="chat-capability-config-card__actions" size={8} wrap>
            <Button
              size="small"
              type="primary"
              disabled={continueDisabled}
              loading={continueLoading}
              onClick={onContinue}
            >
              {t("chat.continueAfterConfiguration")}
            </Button>
          </Space>
        </div>
      )}
    />
  );
}
