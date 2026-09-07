import { useEffect, useState } from "react";
import { Alert, Button, Input, Space, Typography } from "antd";
import { useTranslation } from "react-i18next";
import { Link } from "react-router-dom";
import "./index.scss";

export interface MailDraftPreview {
  draft_id?: string;
  revision?: number;
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
  delivery_unknown?: boolean;
}

export interface MailDraftPatch {
  to?: string;
  cc?: string;
  subject?: string;
  body?: string;
}

interface MailDraftCardProps {
  draft: MailDraftPreview;
  disabled?: boolean;
  onConfirm: (draftId: string, revision: number, patch?: MailDraftPatch) => void;
}

function formatMailTime(value: string) {
  const text = String(value || "").trim();
  if (!text) {
    return "";
  }
  const parsed = new Date(text);
  if (Number.isNaN(parsed.getTime())) {
    return text;
  }
  return parsed.toLocaleString();
}

export default function MailDraftCard({
  draft,
  disabled,
  onConfirm,
}: MailDraftCardProps) {
  const { t } = useTranslation();
  const draftId = String(draft.draft_id || "").trim();
  const revision = Number(draft.revision || 1);
  const sent = draft.status === "sent";
  const deliveryUnknown =
    draft.status === "delivery_unknown" || Boolean(draft.delivery_unknown);
  const failed = !sent && !deliveryUnknown && (draft.status === "failed" || Boolean(draft.last_error));
  const editable = !sent && !disabled;
  const [to, setTo] = useState((draft.to || []).join(", "));
  const [cc, setCc] = useState((draft.cc || []).join(", "));
  const [subject, setSubject] = useState(draft.subject || "");
  const [body, setBody] = useState(draft.body || "");

  useEffect(() => {
    setTo((draft.to || []).join(", "));
    setCc((draft.cc || []).join(", "));
    setSubject(draft.subject || "");
    setBody(draft.body || "");
  }, [draft.body, draft.cc, draft.draft_id, draft.revision, draft.subject, draft.to]);

  const patch: MailDraftPatch = { to, cc, subject, body };
  const hasRecipient = Boolean(to.trim());

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
          <dd>
            {editable ? (
              <Input value={to} onChange={(event) => setTo(event.target.value)} />
            ) : (
              (draft.to || []).join(", ") || "-"
            )}
          </dd>
        </div>
        <div>
          <dt>{t("chat.mailDraft.cc")}</dt>
          <dd>
            {editable ? (
              <Input value={cc} onChange={(event) => setCc(event.target.value)} />
            ) : (
              (draft.cc || []).join(", ") || "-"
            )}
          </dd>
        </div>
        <div>
          <dt>{t("chat.mailDraft.subject")}</dt>
          <dd>
            {editable ? (
              <Input value={subject} onChange={(event) => setSubject(event.target.value)} />
            ) : (
              draft.subject || "-"
            )}
          </dd>
        </div>
        <div>
          <dt>{t("chat.mailDraft.body")}</dt>
          <dd>
            {editable ? (
              <Input.TextArea
                autoSize={{ minRows: 4, maxRows: 12 }}
                value={body}
                onChange={(event) => setBody(event.target.value)}
              />
            ) : (
              <pre>{draft.body || ""}</pre>
            )}
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
          message={t("chat.mailDraft.sentAt", { time: formatMailTime(draft.sent_at || "") })}
        />
      ) : null}
      {!hasRecipient && !sent ? (
        <Alert type="error" showIcon message={t("chat.mailDraft.recipientRequired")} />
      ) : null}
      {failed ? (
        <Alert type="error" showIcon message={draft.last_error || t("chat.mailDraft.sendFailed")} />
      ) : null}
      {deliveryUnknown ? (
        <Alert
          type="warning"
          showIcon
          message={draft.last_error || t("chat.mailDraft.deliveryUnknown")}
        />
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
          {failed || deliveryUnknown ? (
            <Button
              type="primary"
              disabled={!draftId || !hasRecipient}
              onClick={() => onConfirm(draftId, revision, patch)}
            >
              {deliveryUnknown ? t("chat.mailDraft.resendAnyway") : t("chat.mailDraft.resend")}
            </Button>
          ) : (
            <Button
              type="primary"
              disabled={!draftId || !hasRecipient}
              onClick={() => onConfirm(draftId, revision, patch)}
            >
              {t("chat.mailDraft.confirmSend")}
            </Button>
          )}
        </Space>
      ) : null}
    </div>
  );
}
