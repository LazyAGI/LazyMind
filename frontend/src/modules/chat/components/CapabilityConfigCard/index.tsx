import { useEffect, useState } from "react";
import { listToolAssets } from "@/modules/memory/toolApi";
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
  const [checking, setChecking] = useState(true);
  const [availability, setAvailability] = useState<Map<string, boolean> | null>(null);
  useEffect(() => {
    if (!detail) return;
    let active = true;
    let request = 0;
    setAvailability(null);
    const refresh = async () => {
      const current = ++request;
      setChecking(true);
      try {
        const tools = await listToolAssets({ silentError: true });
        if (active && current === request) {
          setAvailability(new Map(tools.map((tool) => [tool.id, tool.isAvailable === true])));
        }
      } catch {
        // Retain the last known requirements when the live check is unavailable.
      } finally {
        if (active && current === request) setChecking(false);
      }
    };
    const onVisible = () => {
      if (document.visibilityState === "visible") void refresh();
    };
    void refresh();
    window.addEventListener("focus", refresh);
    document.addEventListener("visibilitychange", onVisible);
    return () => {
      active = false;
      window.removeEventListener("focus", refresh);
      document.removeEventListener("visibilitychange", onVisible);
    };
  }, [detail, location.pathname, location.search]);
  const missing = (detail?.missing ?? []).filter((item) => availability?.get(item.id) !== true);
  const ready = missing.length === 0;

  if (!detail) {
    return null;
  }

  return (
    <Alert
      className="chat-capability-config-card"
      type={ready ? "success" : "warning"}
      showIcon
      message={checking ? t("common.loading") : ready ? t("chat.mediaCapabilitiesConfigured") : missing.length === 1
        ? t("chat.mediaCapabilityRequiredTitle", { capability: missing[0].label })
        : t("chat.mediaCapabilitiesRequiredTitle")}
      description={(
        <div className="chat-capability-config-card__body">
          <p>{checking ? t("common.loading") : ready ? t("chat.mediaCapabilitiesConfiguredDesc") : t("chat.mediaCapabilitiesRequiredDesc")}</p>
          <div className="chat-capability-config-card__requirements">
            {!checking && missing.map((item) => (
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
              disabled={continueDisabled || checking}
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
