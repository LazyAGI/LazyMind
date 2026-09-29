import { useRef, useState } from "react";
import { Alert, Button, Space, Typography } from "antd";
import { useTranslation } from "react-i18next";
import type { MailDraftPreview } from "./index";
import "./index.scss";

export interface MailMailboxChoice {
  email?: string;
  provider?: string;
}

interface MailMailboxCardProps {
  draft: MailDraftPreview;
  disabled?: boolean;
  onConfirm: (mailbox: string, draftId: string) => boolean | void | Promise<boolean | void>;
}

function mailboxChoices(draft: MailDraftPreview): MailMailboxChoice[] {
  const rows = Array.isArray(draft.mailboxes) ? draft.mailboxes : [];
  const seen = new Set<string>();
  const out: MailMailboxChoice[] = [];
  for (const row of rows) {
    const email = String(row?.email || "").trim();
    if (!email || seen.has(email.toLowerCase())) {
      continue;
    }
    seen.add(email.toLowerCase());
    out.push({
      email,
      provider: String(row?.provider || "").trim(),
    });
  }
  return out;
}

export default function MailMailboxCard({
  draft,
  disabled,
  onConfirm,
}: MailMailboxCardProps) {
  const { t } = useTranslation();
  const [selected, setSelected] = useState("");
  const submittingRef = useRef(false);
  const [submitting, setSubmitting] = useState(false);
  const [submitted, setSubmitted] = useState(false);
  const [submitFailed, setSubmitFailed] = useState(false);
  const draftId = String(draft.draft_id || "").trim();
  const choices = mailboxChoices(draft);
  const locked = disabled || submitting || submitted;
  const handleConfirm = async () => {
    if (locked || submittingRef.current || !choices.some((choice) => choice.email === selected)) return;
    submittingRef.current = true;
    setSubmitting(true);
    setSubmitFailed(false);
    try {
      setSubmitted((await onConfirm(selected, draftId)) !== false);
    } catch {
      setSubmitFailed(true);
    } finally {
      submittingRef.current = false;
      setSubmitting(false);
    }
  };

  return (
    <div className="mail-draft-card mail-mailbox-card">
      <Typography.Title level={5}>{t("chat.mailMailbox.title")}</Typography.Title>
      <Typography.Paragraph type="secondary">
        {t("chat.mailMailbox.description")}
      </Typography.Paragraph>
      {choices.length ? (
        <Space direction="vertical" style={{ width: "100%" }}>
          {choices.map((item) => {
            const email = item.email || "";
            const active = selected === email;
            return (
              <Button
                key={email}
                type={active ? "primary" : "default"}
                block
                disabled={locked}
                onClick={() => setSelected(email)}
              >
                {item.provider ? `${email} (${item.provider})` : email}
              </Button>
            );
          })}
        </Space>
      ) : (
        <Typography.Paragraph type="secondary">
          {t("chat.mailMailbox.empty")}
        </Typography.Paragraph>
      )}
      {submitFailed ? <Alert type="error" message={t("chat.mailDraft.submitFailed")} /> : null}
      {!disabled && !submitted ? (
        <Button
          type="primary"
          style={{ marginTop: 16 }}
          aria-label={t("chat.mailMailbox.confirm")}
          loading={submitting}
          disabled={!draftId || !choices.some((choice) => choice.email === selected) || submitting}
          onClick={() => void handleConfirm()}
        >
          {t("chat.mailMailbox.confirm")}
        </Button>
      ) : null}
    </div>
  );
}
