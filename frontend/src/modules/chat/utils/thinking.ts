const TP_CLOSE_TAG = "</tp>";
const TRP_CLOSE_TAG = "</trp>";

const TP_PAIR_RE = /<tp\b[^>]*>([\s\S]*?)<\/tp>/g;
const TRP_PAIR_RE = /<trp\b[^>]*>([\s\S]*?)<\/trp>/g;
const TOOL_PAYLOAD_PAIR_RE = /<(tool_call|tool_result)>[\s\S]*?<\/\1>/g;
const UNFINISHED_TOOL_PAYLOAD_RE = /<(?:tool_call|tool_result)>[\s\S]*$/g;
const ORPHAN_TAG_RE = /<\/?(?:tp|trp)\b[^>]*>/g;
const ORPHAN_TOOL_PAYLOAD_TAG_RE = /<\/?(?:tool_call|tool_result)>/g;
const MULTIPLE_BLANK_LINES_RE = /\n{3,}/g;
const THINKING_BLOCK_BREAK_RE = /<\/(?:tp|trp)>\s*<(?:tp|trp)\b[^>]*>/g;

export interface ThinkingSplitResult {
  content: string;
  reasoning_content: string;
}

function stripToolPayloads(rawText: string): string {
  return rawText
    .replace(TOOL_PAYLOAD_PAIR_RE, "")
    .replace(UNFINISHED_TOOL_PAYLOAD_RE, "")
    .replace(ORPHAN_TOOL_PAYLOAD_TAG_RE, "");
}

function lastBoundaryIndex(rawText: string): number {
  const lastTrp = rawText.lastIndexOf(TRP_CLOSE_TAG);
  if (lastTrp >= 0) {
    return lastTrp + TRP_CLOSE_TAG.length;
  }
  const lastTp = rawText.lastIndexOf(TP_CLOSE_TAG);
  if (lastTp >= 0) {
    return lastTp + TP_CLOSE_TAG.length;
  }
  return -1;
}

export function hasThinkingPreviewTags(rawText?: string): boolean {
  if (!rawText) {
    return false;
  }
  return rawText.includes("<tp") || rawText.includes("<trp");
}

export function splitThinkingContent(
  rawText?: string,
  fallbackReasoningContent?: string,
): ThinkingSplitResult {
  const text = stripToolPayloads(rawText || "");
  const boundary = lastBoundaryIndex(text);
  if (boundary >= 0) {
    return {
      reasoning_content: text.slice(0, boundary),
      content: text.slice(boundary),
    };
  }
  return {
    reasoning_content: fallbackReasoningContent || "",
    content: text,
  };
}

/**
 * Normalize reasoning_content for display in MarkdownViewer.
 *
 * The streamed reasoning text comes as a sequence of inline tags, e.g.:
 *   <tp id="...">step1</tp><trp id="...">result1</trp><tp id="...">step2</tp>
 *
 * We strip all tp/trp tags and let \n\n paragraph breaks drive markdown
 * paragraph creation, so react-markdown creates <p> elements naturally.
 * This lets markdown formatting inside thinking blocks be parsed and lets
 * CSS control paragraph spacing precisely.
 */
export function formatThinkingForDisplay(rawText?: string): string {
  if (!rawText) {
    return "";
  }

  let result = rawText;

  // Structured tool payloads are machine-readable carriers paired with the
  // human-readable tp/trp previews. Never expose their raw JSON in the UI.
  result = stripToolPayloads(result);

  // Make adjacent thinking blocks explicit before stripping tags so
  // markdown does not merge "...content</tp><trp>Found..." into one line.
  result = result.replace(THINKING_BLOCK_BREAK_RE, "\n\n");

  // Closed <tp>/<trp> pairs -> extract inner content.
  result = result.replace(TP_PAIR_RE, (_match, content: string) => content.trim());
  result = result.replace(TRP_PAIR_RE, (_match, content: string) => content.trim());

  // Any leftover opening/closing tag (typical during streaming when the
  // closing tag has not arrived yet) becomes a paragraph break.
  result = result.replace(ORPHAN_TAG_RE, "\n\n");

  // Collapse 3+ consecutive newlines into a single paragraph break.
  result = result.replace(MULTIPLE_BLANK_LINES_RE, "\n\n");

  return result.trim();
}

const THINK_BLOCK_RE = /<think>[\s\S]*?<\/think>/g;

export function stripThinkTags(rawText?: string): string {
  if (!rawText) {
    return "";
  }
  return rawText.replace(THINK_BLOCK_RE, "").trim();
}
