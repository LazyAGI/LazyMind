import { splitThinkingContent } from './thinking';

/**
 * Export ranges address the finalized body after the last tool round, in UTF-16.
 * The displayed reply may also contain commentary from earlier tool rounds.
 * Decode the same body for preview, download and saving, including restored
 * history. A final export snapshot already contains only that body.
 */
export function chatExportContent(content: string, rawContent?: string): string {
  return rawContent === undefined ? content : splitThinkingContent(rawContent).content;
}
