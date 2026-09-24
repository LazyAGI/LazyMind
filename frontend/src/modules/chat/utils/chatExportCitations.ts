import MarkdownIt from "markdown-it";
import { getDisplaySources, getSourceHref, getSourceLabel, findSourceByCitationId, parseSourceCitationIds, type ChatSourceCollection } from "./sourceAdapter";

// Keep the original Markdown intact; only replace citation links recognized by
// the inline parser (never fenced code, inline code, or escaped examples).
export function exportCitationMarkdown(content: string, sources: ChatSourceCollection, origin: string): string {
  const available = getDisplaySources(sources);
  const references = new Map<string, string>();
  const edits: Array<{ start: number; end: number; text: string }> = [];
  const escape = (text: string) => text.replace(/[\\`*_[\]<>#]/g, "\\$&");
  const parser: any = new MarkdownIt();
  parser.inline.ruler.before("link", "export_citation", (state: any, silent: boolean) => {
    const match = /^\[([^\]\n]*)\]\((#(?:user-content-)?source-[^\s)]+)(?:\s+"[^"\n]*")?\)/.exec(state.src.slice(state.pos));
    if (!match) return false;
    if (!silent) {
      const links = parseSourceCitationIds(match[2]).map((id) => {
        const source = findSourceByCitationId(available, id);
        if (!source) return `${escape(match[1] || id)}（来源信息缺失）`;
        const href = getSourceHref(source);
        let url = "";
        try {
          const target = new URL(href, origin);
          if (!href.startsWith("#") && ["http:", "https:"].includes(target.protocol)) url = target.href;
        } catch { /* Keep source evidence even when its URL is unavailable. */ }
        const label = escape(getSourceLabel(source));
        const link = url ? `[${escape(match[1] || id)}](${url.replace(/[()]/g, (c) => c === "(" ? "%28" : "%29")})` : `${escape(match[1] || id)}（${label}）`;
        references.set(id, `- ${escape(id)}：${label}${url ? ` — <${url}>` : ""}${source.content ? `\n\n  ${escape(source.content).replace(/\r?\n/g, "\n  ")}` : ""}`);
        return link;
      });
      state.env.matches.push({ start: state.pos, end: state.pos + match[0].length, text: links.join(" ") });
      state.push("text", "", 0).content = match[0];
    }
    state.pos += match[0].length;
    return true;
  });
  const lines = content.split("\n");
  const offsets: number[] = [];
  let offset = 0;
  for (const line of lines) { offsets.push(offset); offset += line.length + 1; }
  // Block parsing supplies source line ranges without rendering/reformatting.
  const tokens: any[] = [];
  parser.block.parse(content, parser, {}, tokens);
  let blockStart = 0;
  let cursor = 0;
  for (const token of tokens) {
    if (token.map) blockStart = offsets[token.map[0]];
    if (token.type !== "inline") continue;
    const matches: Array<{ start: number; end: number; text: string }> = [];
    parser.parseInline(token.content, { matches });
    const positions: number[] = [];
    let search = Math.max(blockStart, cursor);
    for (const line of token.content.split("\n")) {
      const found = content.indexOf(line, search);
      if (found < 0) break;
      for (let i = 0; i <= line.length; i++) positions.push(found + i);
      search = found + line.length + 1;
    }
    for (const match of matches) {
      if (positions[match.start] !== undefined && positions[match.end - 1] !== undefined) {
        edits.push({ start: positions[match.start], end: positions[match.end - 1] + 1, text: match.text });
      }
    }
    cursor = search - 1;
  }
  let result = content;
  for (const edit of edits.reverse()) result = result.slice(0, edit.start) + edit.text + result.slice(edit.end);
  return references.size ? `${result}\n\n## 参考资料\n\n${[...references.values()].join("\n\n")}\n` : result;
}
