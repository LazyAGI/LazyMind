import type { SendMessageParams } from "../components/ChatInput/types";

export type MailConfirmationMetadata = {
  mail_draft_confirm_id: string;
  mail_draft_confirm_revision: number;
};
type MailConfirmationPayload = MailConfirmationMetadata & Pick<SendMessageParams, "mail_draft_patch">;

export function mailConfirmationMetadata(value: unknown): MailConfirmationMetadata | undefined {
  const data = value as Partial<MailConfirmationMetadata> | undefined;
  if (!data || typeof data.mail_draft_confirm_id !== "string" || !data.mail_draft_confirm_id.trim() ||
      !Number.isSafeInteger(data.mail_draft_confirm_revision) || (data.mail_draft_confirm_revision ?? 0) < 1) return;
  return { mail_draft_confirm_id: data.mail_draft_confirm_id,
    mail_draft_confirm_revision: data.mail_draft_confirm_revision! };
}

// Payloads never enter message caches/history/storage. Eviction requires a fresh preview.
export class MailConfirmationReplayStore {
  private entries = new Map<string, { conversation: string; turn: string; json: string }>();
  private bytes = 0;
  constructor(private maxEntries = 8, private maxBytes = 64 * 1024 * 1024) {}

  put(conversation: string, turn: string, payload: MailConfirmationPayload): string {
    const key = crypto.randomUUID();
    const json = JSON.stringify({ ...mailConfirmationMetadata(payload), mail_draft_patch: payload.mail_draft_patch });
    const bytes = json.length * 2;
    if (bytes > this.maxBytes) return key;
    while (this.entries.size && (this.entries.size >= this.maxEntries || this.bytes + bytes > this.maxBytes)) {
      const oldest = this.entries.keys().next().value!;
      this.bytes -= this.entries.get(oldest)!.json.length * 2;
      this.entries.delete(oldest);
    }
    this.entries.set(key, { conversation, turn, json });
    this.bytes += bytes;
    return key;
  }

  get(key: string, conversation: string, turn: string, metadata: MailConfirmationMetadata): MailConfirmationPayload | undefined {
    const entry = this.entries.get(key);
    if (!entry || entry.conversation !== conversation || entry.turn !== turn) return;
    const payload = JSON.parse(entry.json) as MailConfirmationPayload;
    if (payload.mail_draft_confirm_id !== metadata.mail_draft_confirm_id ||
        payload.mail_draft_confirm_revision !== metadata.mail_draft_confirm_revision) return;
    return payload;
  }
}

export function mailConfirmationTurnSignature(message: any): string {
  return JSON.stringify([message.delta, message.inputs]);
}
