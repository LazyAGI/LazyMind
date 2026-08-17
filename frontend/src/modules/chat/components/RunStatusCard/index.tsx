import { Alert } from "antd";
import { useTranslation } from "react-i18next";

export interface RunTerminalView {
  status: "completed" | "interrupted" | "failed" | "cancelled";
  reason: string;
  partial_output: boolean;
}

export default function RunStatusCard({
  terminal,
}: {
  terminal?: RunTerminalView;
}) {
  const { t } = useTranslation();
  if (!terminal || terminal.status === "completed") {
    return null;
  }
  const description = terminal.partial_output
    ? t("chat.runStatus.partialOutput")
    : t("chat.runStatus.noOutput");
  return (
    <Alert
      className="chat-run-status-card"
      type={terminal.status === "cancelled" ? "warning" : "error"}
      showIcon
      message={t(`chat.runStatus.${terminal.status}`)}
      description={description}
    />
  );
}
