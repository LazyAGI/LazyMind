import { Typography } from "antd";
import { CheckOutlined, CopyOutlined } from "@ant-design/icons";
import { useTranslation } from "react-i18next";
import { buildAgentRepairRequest, type AgentRepairContext } from "./repairRequest";

export default function RepairRequestCopy(context: AgentRepairContext) {
  const { t } = useTranslation();
  const label = t("agentIntegration.setupFeedback.copyRepairRequest");
  return <Typography.Text copyable={{
    text: buildAgentRepairRequest(context, t),
    icon: [<span><CopyOutlined /> {label}</span>, <span><CheckOutlined /> {label}</span>],
    tooltips: [t("agentIntegration.repairRequest.copyHint"), t("agentIntegration.repairRequest.copied")],
  }} />;
}
