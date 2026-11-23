import { useMemo } from "react";
import katex from "katex";
import "katex/dist/katex.min.css";

interface Segment {
  text: string;
  start: number;
  end: number;
  latex?: string;
  display?: boolean;
}
const unsafeCommand =
  /\\(?:href|url|includegraphics|html(?:Class|Id|Style|Data))\b/;
export function guideTextSegments(text: string): Segment[] {
  const segments: Segment[] = [];
  const escaped = new Uint8Array(text.length);
  for (let index = 1; index < text.length; index++) {
    escaped[index] = text[index - 1] === "\\" && !escaped[index - 1] ? 1 : 0;
  }
  let cursor = 0,
    points = 0;
  const append = (end: number, latex?: string, display?: boolean) => {
    const raw = text.slice(cursor, end);
    const next = points + Array.from(raw).length;
    segments.push({ text: raw, start: points, end: next, latex, display });
    cursor = end;
    points = next;
  };
  for (let i = 0; i < text.length; i++) {
    if (escaped[i]) continue;
    const open = text.startsWith("\\(", i)
      ? "\\("
      : text.startsWith("\\[", i)
        ? "\\["
        : text.startsWith("$$", i)
          ? "$$"
          : text[i] === "$"
            ? "$"
            : "";
    if (!open) continue;
    const close = open === "\\(" ? "\\)" : open === "\\[" ? "\\]" : open;
    let end = text.indexOf(close, i + open.length);
    while (end >= 0 && escaped[end])
      end = text.indexOf(close, end + close.length);
    if (end < 0) {
      // An unclosed compound opener owns the remaining literal text. Never
      // reinterpret its second dollar (or later inner math) as a new formula.
      if (open.length > 1) break;
      continue;
    }
    const latex = text.slice(i + open.length, end);
    if (
      !latex.trim() ||
      (open === "$" && (/\n/.test(latex) || /^\s|\s$/.test(latex)))
    )
      continue;
    if (i > cursor) append(i);
    append(end + close.length, latex, open === "\\[" || open === "$$");
    i = cursor - 1;
  }
  if (cursor < text.length) append(text.length);
  return segments;
}
export default function PaperGuideText({
  text,
  emphasis,
  sourceOffset = 0,
  mapSource = true,
  displayMath = false,
}: {
  text: string;
  emphasis?: string;
  sourceOffset?: number;
  mapSource?: boolean;
  displayMath?: boolean;
}) {
  const segments = useMemo(
    () => {
      const parsed = guideTextSegments(text);
      const pieces: Segment[] = displayMath && !parsed.some(segment => segment.latex !== undefined)
        ? [{ text, start: 0, end: Array.from(text).length, latex: text.trim(), display: true }]
        : parsed.map(segment => displayMath && parsed.length === 1 && segment.latex !== undefined ? { ...segment, display: true } : segment);
      const phrase = emphasis && text.indexOf(emphasis) === text.lastIndexOf(emphasis) ? emphasis : undefined;
      return pieces.map((segment) => {
        const index = phrase && segment.latex === undefined ? segment.text.indexOf(phrase) : -1;
        const content = phrase && index >= 0
          ? <>{segment.text.slice(0, index)}<strong>{phrase}</strong>{segment.text.slice(index + phrase.length)}</>
          : segment.text;
        let html: string | undefined;
        if (
          segment.latex !== undefined &&
          segment.latex.length <= 4096 &&
          !unsafeCommand.test(segment.latex)
        ) {
          try {
            html = katex.renderToString(segment.latex, {
              displayMode: !!segment.display,
              throwOnError: true,
              trust: false,
              strict: "ignore",
              maxExpand: 1000,
              maxSize: 10,
            });
          } catch {
            /* Preserve the complete original text on invalid or excessive TeX. */
          }
        }
        return { ...segment, html, content };
      });
    },
    [text, displayMath, emphasis],
  );
  return (
    <>
      {segments.map((segment) => (
        <span
          key={segment.start}
          data-guide-source-start={
            mapSource ? sourceOffset + segment.start : undefined
          }
          data-guide-source-end={
            mapSource ? sourceOffset + segment.end : undefined
          }
          {...(segment.html
            ? {
                "data-guide-math": true,
                className: segment.display
                  ? "paper-guide-math is-display"
                  : "paper-guide-math",
                dangerouslySetInnerHTML: { __html: segment.html },
              }
            : { children: segment.content })}
        />
      ))}
    </>
  );
}
