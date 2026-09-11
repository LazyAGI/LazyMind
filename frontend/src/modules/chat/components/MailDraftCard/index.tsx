import { useEffect, useMemo, useRef, useState } from "react";
import { Alert, Button, Input, Space, Typography, message } from "antd";
import { useTranslation } from "react-i18next";
import { Link } from "react-router-dom";
import { useTaskCenterStore } from "@/modules/chat/store/taskCenter";
import { getArtifactFilename } from "@/modules/chat/utils/artifactLinks";
import "./index.scss";

const MAX_MAIL_ATTACHMENT_BYTES = 15 * 1024 * 1024;
const MAX_MAIL_ATTACHMENT_COUNT = 5;
const MAX_MAIL_ATTACHMENT_TOTAL_BYTES = 20 * 1024 * 1024;

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
  accepted_recipients?: string[];
  refused_recipients?: string[];
  mailboxes?: Array<{ email?: string; provider?: string }>;
}

export interface MailDraftUploadedAttachment {
  filename: string;
  content_base64: string;
}

export interface MailDraftPatch {
  to?: string;
  cc?: string;
  subject?: string;
  body?: string;
  attachment_paths?: string[];
  attachments?: MailDraftUploadedAttachment[];
}

export interface MailConversationFile {
  name: string;
}

interface MailDraftCardProps {
  draft: MailDraftPreview;
  disabled?: boolean;
  conversationFiles?: MailConversationFile[];
  onConfirm: (draftId: string, revision: number, patch?: MailDraftPatch) => void;
}

type LocalAttachment = {
  key: string;
  name: string;
  source: "draft" | "upload" | "conversation" | "artifact";
  path?: string;
  content_base64?: string;
};

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

function readFileBase64(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => {
      const result = String(reader.result || "");
      const comma = result.indexOf(",");
      resolve(comma >= 0 ? result.slice(comma + 1) : result);
    };
    reader.onerror = () => reject(reader.error);
    reader.readAsDataURL(file);
  });
}

function attachmentsFromDraft(names: string[] | undefined): LocalAttachment[] {
  return (names || [])
    .map((name) => String(name || "").trim())
    .filter(Boolean)
    .map((name) => ({
      key: `draft:${name}`,
      name,
      source: "draft" as const,
      path: name,
    }));
}

export default function MailDraftCard({
  draft,
  disabled,
  conversationFiles = [],
  onConfirm,
}: MailDraftCardProps) {
  const { t } = useTranslation();
  const fileInputRef = useRef<HTMLInputElement | null>(null);
  const draftId = String(draft.draft_id || "").trim();
  const revision = Number(draft.revision || 1);
  const sent = draft.status === "sent";
  const deliveryUnknown =
    draft.status === "delivery_unknown" || Boolean(draft.delivery_unknown);
  const partialSent = draft.status === "partial_sent";
  const failed =
    !sent &&
    !deliveryUnknown &&
    (draft.status === "failed" || partialSent || Boolean(draft.last_error));
  const editable = !sent && !disabled;
  const [to, setTo] = useState((draft.to || []).join(", "));
  const [cc, setCc] = useState((draft.cc || []).join(", "));
  const [subject, setSubject] = useState(draft.subject || "");
  const [body, setBody] = useState(draft.body || "");
  const [attachments, setAttachments] = useState<LocalAttachment[]>(
    () => attachmentsFromDraft(draft.attachments),
  );
  const artifacts = useTaskCenterStore((state) => {
    const conversationId = state.activeConversationId;
    return conversationId ? (state.artifactsByConversation[conversationId] ?? []) : [];
  });
  const artifactChoices = useMemo(() => {
    const seen = new Set<string>();
    return artifacts
      .map((artifact) => {
        if (artifact.content_type !== "file" && artifact.content_type !== "image") {
          return null;
        }
        const path = String(artifact.value?.path || "").trim();
        if (!path) {
          return null;
        }
        const name = getArtifactFilename(artifact);
        return { name, path };
      })
      .filter((item): item is { name: string; path: string } => {
        if (!item?.name || seen.has(item.name)) {
          return false;
        }
        seen.add(item.name);
        return true;
      });
  }, [artifacts]);

  useEffect(() => {
    setTo((draft.to || []).join(", "));
    setCc((draft.cc || []).join(", "));
    setSubject(draft.subject || "");
    setBody(draft.body || "");
    setAttachments(attachmentsFromDraft(draft.attachments));
  }, [draft.body, draft.cc, draft.draft_id, draft.revision, draft.subject, draft.to, draft.attachments]);

  const patch: MailDraftPatch = {
    to,
    cc,
    subject,
    body,
    attachment_paths: attachments
      .filter((item) => item.source !== "upload")
      .map((item) => item.path || item.name),
    attachments: attachments
      .filter((item) => item.source === "upload" && item.content_base64)
      .map((item) => ({
        filename: item.name,
        content_base64: item.content_base64 || "",
      })),
  };
  const hasRecipient = Boolean(to.trim());
  const attachedNames = new Set(attachments.map((item) => item.name));

  const addNamedFile = (name: string, source: "conversation" | "artifact", path?: string) => {
    const filename = String(name || "").trim();
    if (!filename || attachedNames.has(filename)) {
      return;
    }
    setAttachments((current) => [
      ...current,
      {
        key: `${source}:${filename}`,
        name: filename,
        source,
        path: path || filename,
      },
    ]);
  };

  const handleUpload = async (files: FileList | null) => {
    if (!files?.length) {
      return;
    }
    const currentUploads = attachments.filter((item) => item.source === "upload");
    const next: LocalAttachment[] = [];
    for (const file of Array.from(files)) {
      if (currentUploads.length + next.length >= MAX_MAIL_ATTACHMENT_COUNT) {
        message.error(t("chat.mailDraft.attachmentTooMany"));
        break;
      }
      if (file.size > MAX_MAIL_ATTACHMENT_BYTES) {
        message.error(t("chat.mailDraft.attachmentTooLarge"));
        continue;
      }
      const used = currentUploads.reduce(
        (sum, item) => sum + Math.floor(((item.content_base64 || "").length * 3) / 4),
        0,
      ) + next.reduce((sum, item) => sum + Math.floor(((item.content_base64 || "").length * 3) / 4), 0);
      if (used + file.size > MAX_MAIL_ATTACHMENT_TOTAL_BYTES) {
        message.error(t("chat.mailDraft.attachmentTotalTooLarge"));
        break;
      }
      const content_base64 = await readFileBase64(file);
      next.push({
        key: `upload:${file.name}:${file.size}:${file.lastModified}`,
        name: file.name,
        source: "upload",
        content_base64,
      });
    }
    if (next.length) {
      setAttachments((current) => {
        const names = new Set(current.map((item) => item.name));
        return [...current, ...next.filter((item) => !names.has(item.name))];
      });
    }
    if (fileInputRef.current) {
      fileInputRef.current.value = "";
    }
  };

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
        <div>
          <dt>{t("chat.mailDraft.attachments")}</dt>
          <dd>
            <ul className="mail-draft-attachments">
              {attachments.map((item) => (
                <li key={item.key}>
                  <span>{item.name}</span>
                  {editable ? (
                    <Button
                      type="link"
                      size="small"
                      onClick={() =>
                        setAttachments((current) => current.filter((entry) => entry.key !== item.key))
                      }
                    >
                      {t("chat.mailDraft.removeAttachment")}
                    </Button>
                  ) : null}
                </li>
              ))}
            </ul>
            {editable ? (
              <Space wrap>
                <input
                  ref={fileInputRef}
                  type="file"
                  multiple
                  hidden
                  onChange={(event) => {
                    void handleUpload(event.target.files);
                  }}
                />
                <Button onClick={() => fileInputRef.current?.click()}>
                  {t("chat.mailDraft.uploadAttachment")}
                </Button>
                {conversationFiles.length ? (
                  conversationFiles
                    .filter((file) => file.name && !attachedNames.has(file.name))
                    .map((file) => (
                      <Button
                        key={`chat-${file.name}`}
                        onClick={() => addNamedFile(file.name, "conversation", file.name)}
                      >
                        {t("chat.mailDraft.addFromConversation")}: {file.name}
                      </Button>
                    ))
                ) : (
                  <Typography.Text type="secondary">
                    {t("chat.mailDraft.noConversationFiles")}
                  </Typography.Text>
                )}
                {artifactChoices.length ? (
                  artifactChoices
                    .filter((item) => !attachedNames.has(item.name))
                    .map((item) => (
                      <Button
                        key={`artifact-${item.name}`}
                        onClick={() => addNamedFile(item.name, "artifact", item.path)}
                      >
                        {t("chat.mailDraft.addFromArtifacts")}: {item.name}
                      </Button>
                    ))
                ) : (
                  <Typography.Text type="secondary">
                    {t("chat.mailDraft.noArtifacts")}
                  </Typography.Text>
                )}
              </Space>
            ) : attachments.length ? null : (
              "-"
            )}
          </dd>
        </div>
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
      {partialSent ? (
        <Alert type="warning" showIcon message={draft.last_error || t("chat.mailDraft.partialSent")} />
      ) : null}
      {failed && !partialSent ? (
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
