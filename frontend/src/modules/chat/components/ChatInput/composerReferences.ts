import type { ChatMention } from './MentionEditor';
import { RoleTypes } from '@/modules/chat/constants/common';

export interface ComposerFileInput {
  input_type: 'file' | 'image';
  uri: string;
  filename: string;
  input_base64?: string;
}

export interface ComposerFileCandidate {
  id: string;
  name: string;
  input?: ComposerFileInput;
  disabled?: boolean;
}

export function collectConversationFiles(messages: Array<{
  role?: string;
  history_id?: string;
  id?: string;
  trail_source?: string;
  trail_depth?: number;
  inputs?: Array<{ input_type?: string; uri?: string; filename?: string; text?: string; fork_unavailable?: boolean }>;
}>): ComposerFileCandidate[] {
  return messages.flatMap((message) => {
    if (message.role !== RoleTypes.USER || message.trail_source === 'parent' || (message.trail_depth ?? 0) > 0) return [];
    const historyId = message.history_id || message.id;
    if (!historyId) return [];
    return (message.inputs || []).flatMap((input, index) => {
      if (!input.uri || !input.filename || input.fork_unavailable || (input.input_type !== 'file' && input.input_type !== 'image')) return [];
      return [{ id: `history:${historyId}:${index}`, name: input.filename,
        input: { input_type: input.input_type, uri: input.uri, filename: input.filename } }];
    });
  });
}

export function buildComposerReferences(
  selections: ChatMention[],
  files: ComposerFileCandidate[],
  uploads: ComposerFileInput[],
  text = '',
) {
  const inputs = new Map(uploads.map((input) => [input.uri, input]));
  let unavailable = false;
  for (const selection of selections) {
    if (selection.type !== 'file') continue;
    const file = files.find((item) => item.id === selection.resource_id);
    if (!file?.input?.uri || file.disabled) unavailable = true;
    else inputs.set(file.input.uri, file.input);
  }
  const leadingSpace = text.length - text.trimStart().length;
  return { mentions: selections.filter((item) => item.type !== 'file').map(item => leadingSpace ? {
    ...item,
    ...(item.start !== undefined ? { start: Math.max(0, item.start - leadingSpace) } : {}),
    ...(item.end !== undefined ? { end: Math.max(0, item.end - leadingSpace) } : {}),
  } : item), inputs: [...inputs.values()], unavailable };
}
