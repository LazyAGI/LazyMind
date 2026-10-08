import { useEffect, useMemo, useRef, useState, type ChangeEvent } from "react";
import { Alert, Button, Input, Select, Space, Typography, message } from "antd";
import { MailOutlined, PaperClipOutlined, SendOutlined } from "@ant-design/icons";
import { useTranslation } from "react-i18next";
import { Link } from "react-router-dom";
import { useTaskCenterStore, type ConversationArtifact } from "@/modules/chat/store/taskCenter";
import { getArtifactFilename } from "@/modules/chat/utils/artifactLinks";
import type { RealtimeRefusal } from "@/modules/chat/utils/realtimeTransport";
import "./index.scss";

// Base64 attachments travel in one realtime frame (MAX_REALTIME_FRAME_BYTES);
// 10MB raw grows to about 13.4MB and leaves room for the draft text.
const MAX_MAIL_ATTACHMENT_BYTES = 10 * 1024 * 1024;
const MAX_MAIL_ATTACHMENT_COUNT = 5;
const MAX_MAIL_ATTACHMENT_TOTAL_BYTES = 10 * 1024 * 1024;
const EMPTY_MAIL_ARTIFACTS: ConversationArtifact[] = [];
const EMAIL_ADDRESS_PATTERN = /^[^\s@,;<>]+@[^\s@,;<>]+\.[^\s@,;<>]+$/u;

export interface MailDraftPreview {
  draft_id?: string;
  revision?: number;
  mailbox?: string;
  to?: string[];
  cc?: string[];
  subject?: string;
  body?: string;
  attachments?: string[];
  attachment_paths?: string[];
  pending_attachment_names?: string[];
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
  /** A newer card for the same draft carries the authoritative status. */
  superseded?: boolean;
  conversationFiles?: MailConversationFile[];
  onConfirm: (
    draftId: string,
    revision: number,
    patch?: MailDraftPatch,
    onRefused?: (reason: RealtimeRefusal) => void,
  ) => boolean | void | Promise<boolean | void>;
}

const SUBMIT_REFUSAL_MESSAGES: Record<RealtimeRefusal, string> = {
  offline: "chat.mailDraft.submitOffline",
  payload_too_large: "chat.mailDraft.submitTooLarge",
};

type LocalAttachment = {
  key: string;
  name: string;
  source: "draft" | "upload" | "conversation" | "artifact" | "pending";
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

function formatMailError(value: unknown, depth = 0): string {
  if (value == null || value === "" || depth > 8) {
    return "";
  }
  if (typeof value === "object") {
    const record = value as Record<string, unknown>;
    for (const key of ["last_error", "message", "msg", "error", "detail", "reason", "value"]) {
      const nested = formatMailError(record[key], depth + 1);
      if (nested) {
        return nested;
      }
    }
    return "";
  }
  const text = String(value).trim();
  const jsonStart = text.search(/[\[{]/);
  if (jsonStart >= 0) {
    try {
      return formatMailError(JSON.parse(text.slice(jsonStart)), depth + 1);
    } catch {
      return "";
    }
  }
  return text;
}

function stringList(value: unknown): string[] {
  if (Array.isArray(value)) {
    return value.map((item) => String(item ?? "").trim()).filter(Boolean);
  }
  if (typeof value === "string") {
    return value
      .split(/[,;]/)
      .map((item) => item.trim())
      .filter(Boolean);
  }
  return [];
}

function addressList(value: string): string[] {
  return value
    .split(/[,;，；]/)
    .map((item) => item.trim())
    .filter(Boolean);
}

function mergeAddresses(values: string[], search: string): string[] {
  const seen = new Set<string>();
  return [...values, ...addressList(search)]
    .map((address) => address.trim())
    .filter((address) => {
      const key = address.toLowerCase();
      if (!address || seen.has(key)) return false;
      seen.add(key);
      return true;
    });
}

function hasInvalidAddress(addresses: string[]): boolean {
  return addresses.some((address) => !EMAIL_ADDRESS_PATTERN.test(address));
}

function isRecipientValidationError(value: string): boolean {
  const normalized = value.toLowerCase();
  return normalized.includes("no recipients")
    || normalized.includes("no valid recipients")
    || normalized.includes("at least one recipient is required");
}

function attachmentsFromDraft(names: unknown, paths?: unknown, pendingNames?: unknown): LocalAttachment[] {
  const references = stringList(paths);
  const pending = new Set(stringList(pendingNames));
  return stringList(names)
    .map((name) => String(name || "").trim())
    .filter(Boolean)
    .map((name, index) => ({
      key: `draft:${references[index] || name}`,
      name,
      source: pending.has(name) ? "pending" as const : "draft" as const,
      path: references[index] || name,
    }));
}

export default function MailDraftCard({
  draft,
  disabled,
  superseded,
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
  const sending = draft.status === "sending";
  const submittingRef = useRef(false);
  const [submitting, setSubmitting] = useState(false);
  const [submitted, setSubmitted] = useState(false);
  const [submitError, setSubmitError] = useState("");
  const [uploading, setUploading] = useState(false);
  const uploadGeneration = useRef(0);
  const lastError = formatMailError(draft.last_error);
  const staleRecipientError = isRecipientValidationError(lastError);
  const failed =
    !sent &&
    !deliveryUnknown &&
    (draft.status === "failed" || partialSent || Boolean(lastError));
  const editable = !sent && !sending && !disabled && !submitting && !submitted;
  const [to, setTo] = useState<string[]>(() => stringList(draft.to));
  const [toSearch, setToSearch] = useState("");
  const [cc, setCc] = useState<string[]>(() => stringList(draft.cc));
  const [ccSearch, setCcSearch] = useState("");
  const [subject, setSubject] = useState(draft.subject || "");
  const [body, setBody] = useState(draft.body || "");
  const [attachments, setAttachments] = useState<LocalAttachment[]>(
    () => attachmentsFromDraft(draft.attachments, draft.attachment_paths, draft.pending_attachment_names),
  );
  const artifacts = useTaskCenterStore((state) => {
    const conversationId = state.activeConversationId;
    return conversationId ? (state.artifactsByConversation[conversationId] ?? EMPTY_MAIL_ARTIFACTS) : EMPTY_MAIL_ARTIFACTS;
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

  // Stream/history snapshots often contain new arrays with unchanged contents.
  // Only an actual server-side change should replace the user's local edits.
  const snapshotKey = JSON.stringify({
    draftId, revision, status: draft.status, lastError,
    to: stringList(draft.to), cc: stringList(draft.cc),
    subject: draft.subject || "", body: draft.body || "",
    attachments: stringList(draft.attachments),
    attachmentPaths: stringList(draft.attachment_paths),
    pendingAttachmentNames: stringList(draft.pending_attachment_names),
  });
  const snapshot = useMemo(() => JSON.parse(snapshotKey), [snapshotKey]);
  const currentSnapshotKey = useRef(snapshotKey);
  currentSnapshotKey.current = snapshotKey;
  useEffect(() => {
    setTo(snapshot.to);
    setToSearch("");
    setCc(snapshot.cc);
    setCcSearch("");
    setSubject(snapshot.subject);
    setBody(snapshot.body);
    setAttachments(attachmentsFromDraft(snapshot.attachments, snapshot.attachmentPaths, snapshot.pendingAttachmentNames));
    setSubmitted(false);
    setSubmitError("");
    setSubmitting(false);
    submittingRef.current = false;
    uploadGeneration.current += 1;
    setUploading(false);
  }, [snapshot]);

  const toAddresses = mergeAddresses(to, toSearch);
  const ccAddresses = mergeAddresses(cc, ccSearch);
  const patch: MailDraftPatch = {
    to: toAddresses.join(", "),
    cc: ccAddresses.join(", "),
    subject,
    body,
    attachment_paths: attachments
      .filter((item) => item.source !== "upload" && item.source !== "pending")
      .map((item) => item.path || item.name),
    attachments: attachments
      .filter((item) => item.source === "upload" && item.content_base64)
      .map((item) => ({
        filename: item.name,
        content_base64: item.content_base64 || "",
      })),
  };
  const recipientRequired = toAddresses.length === 0;
  const toAddressInvalid = !recipientRequired && hasInvalidAddress(toAddresses);
  const ccAddressInvalid = Boolean(cc.length || ccSearch.trim()) && (
    ccAddresses.length === 0 || hasInvalidAddress(ccAddresses)
  );
  const recipientInvalid = toAddressInvalid || ccAddressInvalid;
  const recipientError = recipientRequired
    ? "chat.mailDraft.recipientRequired"
    : recipientInvalid
      ? "chat.mailDraft.recipientInvalid"
      : "";
  const recipientsValid = !recipientError;
  const hasPendingAttachments = attachments.some((item) => item.source === "pending");
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
    if (!files?.length || uploading) {
      return;
    }
    const generation = uploadGeneration.current;
    setUploading(true);
    try {
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
      if (next.length && generation === uploadGeneration.current) {
        setAttachments((current) => {
          const kept = current.filter((item) => item.source !== "pending" || !next.some((upload) => upload.name === item.name));
          const names = new Set(kept.map((item) => item.name));
          return [...kept, ...next.filter((item) => !names.has(item.name))];
        });
      }
      if (fileInputRef.current) fileInputRef.current.value = "";
    } catch {
      message.error(t("chat.mailDraft.attachmentReadFailed"));
    } finally {
      if (generation === uploadGeneration.current) setUploading(false);
    }
  };

  const handleConfirm = async () => {
    if (!editable || uploading || hasPendingAttachments || submittingRef.current || !draftId || !recipientsValid) return;
    submittingRef.current = true;
    setSubmitting(true);
    setSubmitError("");
    const submittedSnapshotKey = snapshotKey;
    let refused = false;
    const handleRefused = (reason: RealtimeRefusal) => {
      refused = true;
      if (currentSnapshotKey.current !== submittedSnapshotKey) return;
      submittingRef.current = false;
      setSubmitting(false);
      setSubmitted(false);
      setSubmitError(SUBMIT_REFUSAL_MESSAGES[reason] || "chat.mailDraft.submitFailed");
    };
    try {
      const started = await onConfirm(draftId, revision, patch, handleRefused);
      if (currentSnapshotKey.current === submittedSnapshotKey && !refused) {
        setSubmitted(started !== false);
        if (started === false) setSubmitError("chat.mailDraft.submitFailed");
      }
    } catch {
      if (currentSnapshotKey.current === submittedSnapshotKey) setSubmitError("chat.mailDraft.submitFailed");
    } finally {
      if (currentSnapshotKey.current === submittedSnapshotKey) {
        submittingRef.current = false;
        setSubmitting(false);
      }
    }
  };

  return (
    <div className={`mail-draft-card${sent ? " is-sent" : ""}`}>
      <header className="mail-draft-header">
        <span className="mail-draft-header-icon" aria-hidden="true"><MailOutlined /></span>
        <Typography.Title level={5}>{t(sent ? "chat.mailDraft.sentTitle" : "chat.mailDraft.title")}</Typography.Title>
      </header>
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
              <Select
                className="mail-draft-address"
                aria-label={t("chat.mailDraft.to")}
                mode="tags"
                searchValue={toSearch}
                status={recipientRequired || toAddressInvalid ? "error" : undefined}
                value={to}
                suffixIcon={null}
                tokenSeparators={[",", ";", "，", "；"]}
                onChange={(values) => {
                  setTo(mergeAddresses(values, ""));
                  setToSearch("");
                }}
                onInputKeyDown={(event) => {
                  if (event.key === "Enter" && toSearch.trim() && !event.nativeEvent.isComposing) {
                    event.preventDefault();
                    setTo(mergeAddresses(to, toSearch));
                    setToSearch("");
                  }
                }}
                onSearch={setToSearch}
              />
            ) : (
              toAddresses.join(", ") || "-"
            )}
          </dd>
        </div>
        <div>
          <dt>{t("chat.mailDraft.cc")}</dt>
          <dd>
            {editable ? (
              <Select
                className="mail-draft-address"
                aria-label={t("chat.mailDraft.cc")}
                mode="tags"
                searchValue={ccSearch}
                status={ccAddressInvalid ? "error" : undefined}
                value={cc}
                suffixIcon={null}
                tokenSeparators={[",", ";", "，", "；"]}
                onChange={(values) => {
                  setCc(mergeAddresses(values, ""));
                  setCcSearch("");
                }}
                onInputKeyDown={(event) => {
                  if (event.key === "Enter" && ccSearch.trim() && !event.nativeEvent.isComposing) {
                    event.preventDefault();
                    setCc(mergeAddresses(cc, ccSearch));
                    setCcSearch("");
                  }
                }}
                onSearch={setCcSearch}
              />
            ) : (
              ccAddresses.join(", ") || "-"
            )}
          </dd>
        </div>
        <div>
          <dt>{t("chat.mailDraft.subject")}</dt>
          <dd>
            {editable ? (
              <Input aria-label={t("chat.mailDraft.subject")} value={subject} onChange={(event: ChangeEvent<HTMLInputElement>) => setSubject(event.target.value)} />
            ) : (
              subject || "-"
            )}
          </dd>
        </div>
        <div>
          <dt>{t("chat.mailDraft.body")}</dt>
          <dd>
            {editable ? (
              <Input.TextArea
                aria-label={t("chat.mailDraft.body")}
                autoSize={{ minRows: 4, maxRows: 12 }}
                value={body}
                onChange={(event: ChangeEvent<HTMLTextAreaElement>) => setBody(event.target.value)}
              />
            ) : (
              <pre>{body}</pre>
            )}
          </dd>
        </div>
        <div>
          <dt>{t("chat.mailDraft.attachments")}</dt>
          <dd>
            <ul className="mail-draft-attachments">
              {attachments.map((item) => (
                <li key={item.key}>
                  <PaperClipOutlined aria-hidden="true" />
                  <span title={item.name}>{item.name}</span>
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
              <Space wrap className="mail-draft-file-actions">
                <input
                  ref={fileInputRef}
                  type="file"
                  multiple
                  hidden
                  onChange={(event) => {
                    void handleUpload(event.target.files);
                  }}
                />
                <Button loading={uploading} icon={<PaperClipOutlined aria-hidden="true" />} onClick={() => fileInputRef.current?.click()}>
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
      {!superseded && (sending || submitting || submitted) ? (
        <Alert type="info" showIcon message={t("chat.mailDraft.sending")} />
      ) : null}
      {submitError ? <Alert type="error" showIcon message={t(submitError)} /> : null}
      {hasPendingAttachments ? <Alert type="warning" showIcon message={t("chat.mailDraft.attachmentPending")} /> : null}
      {recipientError && !sent ? (
        <Alert type="error" showIcon message={t(recipientError)} />
      ) : null}
      {partialSent && !recipientError ? (
        <Alert type="warning" showIcon message={lastError || t("chat.mailDraft.partialSent")} />
      ) : null}
      {failed && !partialSent && !recipientError && !staleRecipientError ? (
        <Alert type="error" showIcon message={lastError || t("chat.mailDraft.sendFailed")} />
      ) : null}
      {deliveryUnknown ? (
        <Alert
          type="warning"
          showIcon
          message={lastError || t("chat.mailDraft.deliveryUnknown")}
          description={lastError ? t("chat.mailDraft.deliveryUnknown") : undefined}
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
      {editable ? (
        <footer className="mail-draft-footer">
          {failed || deliveryUnknown ? (
            <Button
              type="primary"
              icon={<SendOutlined aria-hidden="true" />}
              disabled={!draftId || !recipientsValid || uploading || hasPendingAttachments}
              onClick={() => void handleConfirm()}
            >
              {deliveryUnknown ? t("chat.mailDraft.resendAnyway") : t("chat.mailDraft.resend")}
            </Button>
          ) : (
            <Button
              type="primary"
              icon={<SendOutlined aria-hidden="true" />}
              disabled={!draftId || !recipientsValid || uploading || hasPendingAttachments}
              onClick={() => void handleConfirm()}
            >
              {t("chat.mailDraft.confirmSend")}
            </Button>
          )}
        </footer>
      ) : null}
    </div>
  );
}
