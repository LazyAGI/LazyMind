import { Button, Modal, message } from "antd";
import { getLocalizedBuiltinSkillDownloadFailures, getLocalizedErrorMessage } from "@/components/request";
import i18n from "@/i18n";

export function showBuiltinSkillInstallError(error: unknown, builtinSkillUid: string) {
  const details = getLocalizedBuiltinSkillDownloadFailures(error);
  if (!details.length) {
    message.error(getLocalizedErrorMessage(error));
    return;
  }

  const title = i18n.t("admin.memoryBuiltinSkillDownloadFailed");
  const closeToast = message.error({
    key: `builtin-skill-install-${builtinSkillUid}`,
    duration: 8,
    content: (
      <>
        {title}
        <Button type="link" size="small" onClick={() => {
          closeToast();
          Modal.info({
            title,
            content: <ul>{details.map((detail) => <li key={detail}>{detail}</li>)}</ul>,
            okText: i18n.t("common.close"),
          });
        }}>
          {i18n.t("admin.memoryBuiltinSkillDownloadDetails")}
        </Button>
      </>
    ),
  });
}
