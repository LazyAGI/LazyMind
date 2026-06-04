import { InfoCircleOutlined } from "@ant-design/icons";
import { Tooltip } from "antd";
import { useTranslation } from "react-i18next";

interface RiskTipProps {
  titleKey: string;
}

export default function RiskTip({ titleKey }: RiskTipProps) {
  const { t } = useTranslation();

  return (
    <Tooltip title={<span>{t(titleKey)}</span>}>
      <InfoCircleOutlined />
    </Tooltip>
  );
}
