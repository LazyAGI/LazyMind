import { useState } from "react";
import { Button, Space, Typography } from "antd";
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
  onConfirm: (mailbox: string, draftId: string) => void;
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
  const draftId = String(draft.draft_id || "").trim();
  const choices = mailboxChoices(draft);

  return (
    <div className="mail-draft-card">
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
                disabled={disabled}
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
      {!disabled ? (
        <Button
          type="primary"
          style={{ marginTop: 16 }}
          disabled={!draftId || !selected}
          onClick={() => onConfirm(selected, draftId)}
        >
          {t("chat.mailMailbox.confirm")}
        </Button>
      ) : null}
    </div>
  );
}
