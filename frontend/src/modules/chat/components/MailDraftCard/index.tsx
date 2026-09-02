import { Alert, Button, Space, Typography } from "antd";
import { useTranslation } from "react-i18next";
import { Link } from "react-router-dom";
import "./index.scss";

export interface MailDraftPreview {
  draft_id?: string;
  mailbox?: string;
  to?: string[];
  cc?: string[];
  subject?: string;
  body?: string;
  attachments?: string[];
  in_reply_to?: string;
  status?: string;
  sent_at?: string;
  last_error?: string;
  requires_confirmation?: boolean;
  error_code?: string;
  requires_reauth?: boolean;
  reauth_path?: string;
}

interface MailDraftCardProps {
  draft: MailDraftPreview;
  disabled?: boolean;
  onConfirm: (draftId: string) => void;
  onRetry?: (draftId: string) => void;
}

export default function MailDraftCard({
  draft,
  disabled,
  onConfirm,
  onRetry,
}: MailDraftCardProps) {
  const { t } = useTranslation();
  const draftId = String(draft.draft_id || "").trim();
  const failed = draft.status === "failed" || Boolean(draft.last_error);
  const sent = draft.status === "sent";

  return (
    <div className="mail-draft-card">
      <Typography.Title level={5}>{t("chat.mailDraft.title")}</Typography.Title>
      <dl>
        {draft.mailbox ? (
          <div>
            <dt>{t("chat.mailDraft.from")}</dt>
            <dd>{draft.mailbox}</dd>
          </div>
        ) : null}
        <div>
          <dt>{t("chat.mailDraft.to")}</dt>
          <dd>{(draft.to || []).join(", ") || "-"}</dd>
        </div>
        {draft.cc?.length ? (
          <div>
            <dt>{t("chat.mailDraft.cc")}</dt>
            <dd>{draft.cc.join(", ")}</dd>
          </div>
        ) : null}
        <div>
          <dt>{t("chat.mailDraft.subject")}</dt>
          <dd>{draft.subject || "-"}</dd>
        </div>
        <div>
          <dt>{t("chat.mailDraft.body")}</dt>
          <dd>
            <pre>{draft.body || ""}</pre>
          </dd>
        </div>
        {draft.attachments?.length ? (
          <div>
            <dt>{t("chat.mailDraft.attachments")}</dt>
            <dd>{draft.attachments.join(", ")}</dd>
          </div>
        ) : null}
      </dl>
      {sent ? (
        <Alert
          type="success"
          showIcon
          message={t("chat.mailDraft.sentAt", { time: draft.sent_at || "" })}
        />
      ) : null}
      {failed ? (
        <Alert type="error" showIcon message={draft.last_error || t("chat.mailDraft.sendFailed")} />
      ) : null}
      {draft.requires_reauth ? (
        <Alert
          type="warning"
          showIcon
          message={t("chat.mailDraft.reauthRequired")}
          action={
            <Link to={draft.reauth_path || "/cloud-documents/mail"}>
              {t("chat.mailDraft.reauth")}
            </Link>
          }
        />
      ) : null}
      {!sent && !disabled ? (
        <Space>
          <Button type="primary" disabled={!draftId} onClick={() => onConfirm(draftId)}>
            {t("chat.mailDraft.confirmSend")}
          </Button>
          {failed ? (
            <Button disabled={!draftId} onClick={() => (onRetry || onConfirm)(draftId)}>
              {t("common.retry")}
            </Button>
          ) : null}
        </Space>
      ) : null}
    </div>
  );
}
