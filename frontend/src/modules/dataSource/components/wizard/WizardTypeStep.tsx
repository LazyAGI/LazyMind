import type { ReactNode } from "react";
import { Button, Space, Tag, Typography } from "antd";
import {
  ApiOutlined,
  DatabaseOutlined,
  DisconnectOutlined,
  FolderOpenOutlined,
  LockOutlined,
} from "@ant-design/icons";
import type { TFunction } from "i18next";
import type { SourceType } from "../../constants/types";
import { getSourceTypeDescription, getSourceTypeTitle } from "../../utils/status";

const { Paragraph, Text } = Typography;

const sourceTypeOptions: Array<{
  type: SourceType;
  icon: ReactNode;
  adminOnly?: boolean;
}> = [
  {
    type: "local",
    icon: <FolderOpenOutlined />,
    adminOnly: true,
  },
  {
    type: "feishu",
    icon: <ApiOutlined />,
  },
  {
    type: "notion",
    icon: <DatabaseOutlined />,
  },
];

export interface WizardTypeStepProps {
  t: TFunction;
  selectedType: SourceType | null;
  isFeishuSetupReady: boolean;
  isNotionSetupReady: boolean;
  onSelectType: (type: SourceType) => void;
  onResetFeishuSetup: () => void;
  onResetNotionSetup?: () => void;
}

export default function WizardTypeStep({
  t,
  selectedType,
  isFeishuSetupReady,
  isNotionSetupReady,
  onSelectType,
  onResetFeishuSetup,
  onResetNotionSetup,
}: WizardTypeStepProps) {
  return (
    <div>
      <Paragraph type="secondary" className="data-source-wizard-intro">
        {t("admin.dataSourceTypeStepIntro")}
      </Paragraph>
      <div className="data-source-type-grid">
        {sourceTypeOptions.map((item) => {
          const isFeishuLocked = item.type === "feishu" && !isFeishuSetupReady;
          const isNotionLocked = item.type === "notion" && !isNotionSetupReady;
          const isCloudLocked = isFeishuLocked || isNotionLocked;
          return (
            <button
              key={item.type}
              type="button"
              className={`data-source-type-card ${
                selectedType === item.type ? "selected" : ""
              } ${isCloudLocked ? "locked" : ""}`}
              onClick={() => onSelectType(item.type)}
            >
              <div className="data-source-type-card-header">
                <span className={`data-source-icon data-source-icon-${item.type}`}>
                  {item.icon}
                </span>
                <Space size={6}>
                  {item.type === "feishu" || item.type === "notion" ? (
                    isCloudLocked ? (
                      <span className="data-source-type-gate-icon locked" aria-hidden="true">
                        <LockOutlined />
                      </span>
                    ) : (
                      <Button
                        type="text"
                        size="small"
                        className="data-source-type-gate-button"
                        icon={<DisconnectOutlined />}
                        onClick={(event) => {
                          event.preventDefault();
                          event.stopPropagation();
                          if (item.type === "feishu") {
                            onResetFeishuSetup();
                          } else {
                            onResetNotionSetup?.();
                          }
                        }}
                      />
                    )
                  ) : null}
                  {item.adminOnly ? (
                    <Tag color="orange">{t("admin.dataSourceAdminOnly")}</Tag>
                  ) : null}
                </Space>
              </div>
              <Text strong>{getSourceTypeTitle(item.type, t)}</Text>
              <Text type="secondary">
                {item.type === "feishu" && isFeishuLocked
                  ? t("admin.dataSourceFeishuLockHint")
                  : item.type === "notion" && isNotionLocked
                    ? t("admin.dataSourceNotionSetupRequiredForCreate")
                  : getSourceTypeDescription(item.type, t)}
              </Text>
            </button>
          );
        })}
      </div>
    </div>
  );
}
