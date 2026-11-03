import type { TextContent, TextItem } from 'pdfjs-dist/types/src/display/api';

interface TextRun {
  text: string;
  x: number;
  y: number;
  size: number;
  width: number;
}

interface TextLine {
  text: string;
  left: number;
  right: number;
  top: number;
  size: number;
}

// PDF font changes can split a single word into several text items. Compare
// complete title blocks without making those artificial boundaries significant.
function titleKey(value: string): string {
  return value.normalize('NFKC').toLowerCase().replace(/[^\p{L}\p{N}]/gu, '');
}

export function pdfTitlesMatch(actual: string | undefined, expected: string): boolean {
  const key = actual ? titleKey(actual) : '';
  return !!key && key === titleKey(expected);
}

function textLines(items: TextContent['items'], view: readonly number[]): TextLine[] {
  const pageHeight = view[3] - view[1];
  if (!(pageHeight > 0)) return [];
  const runs: TextRun[] = items
    .filter((item): item is TextItem => 'str' in item && !!item.str.trim())
    .map(item => {
      const [a, b, c, d, x, y] = item.transform;
      return {
        text: item.str, x: x - view[0], y: y - view[1], width: item.width,
        size: Math.max(Math.hypot(a, b), Math.hypot(c, d)),
        angle: Math.atan2(b, a),
      };
    })
    // Rotated arXiv stamps are useful for identifiers, but are not title lines.
    .filter(run => Math.abs(run.angle) < 0.15 && run.size > 0
      && [run.x, run.y, run.size, run.width].every(Number.isFinite));

  const rows: Array<{ y: number; size: number; runs: TextRun[] }> = [];
  for (const run of runs.sort((a, b) => b.y - a.y || a.x - b.x)) {
    let row = rows.find(candidate => Math.abs(candidate.y - run.y) <= Math.max(1.7, Math.min(candidate.size, run.size) * 0.25));
    if (!row) {
      row = { y: run.y, size: run.size, runs: [] };
      rows.push(row);
    }
    row.runs.push(run);
    row.size = Math.max(row.size, run.size);
  }

  const lines: TextLine[] = [];
  for (const row of rows.sort((a, b) => b.y - a.y)) {
    let group: TextRun[] = [];
    const finishLine = () => {
      if (!group.length) return;
      let text = '';
      let previous: TextRun | undefined;
      for (const run of group) {
        if (previous) {
          const gap = run.x - previous.x - previous.width;
          const adjacentHan = /\p{Script=Han}$/u.test(previous.text) && /^\p{Script=Han}/u.test(run.text);
          if (!/\s$/.test(previous.text) && !/^\s/.test(run.text) && !adjacentHan
            && gap > Math.min(previous.size, run.size) * 0.16) text += ' ';
        }
        text += run.text;
        previous = run;
      }
      const characters = group.reduce((sum, run) => sum + run.text.replace(/\s/g, '').length, 0);
      lines.push({
        text: text.trim(), left: group[0].x,
        right: Math.max(...group.map(run => run.x + run.width)),
        top: pageHeight - row.y,
        size: group.reduce((sum, run) => sum + run.size * run.text.replace(/\s/g, '').length, 0) / characters,
      });
      group = [];
    };
    for (const run of row.runs.sort((a, b) => a.x - b.x)) {
      const previous = group[group.length - 1];
      if (previous) {
        const gap = run.x - previous.x - previous.width;
        const sizeRatio = Math.min(run.size, previous.size) / Math.max(run.size, previous.size);
        // A sidebar may share the title's baseline despite belonging to a
        // different column. Keep small-caps fragments within the same word.
        if (gap > Math.max(run.size, previous.size) * 4
          || (sizeRatio < 0.65 && gap > Math.min(run.size, previous.size) * 0.7)) finishLine();
      }
      group.push(run);
    }
    finishLine();
  }
  return lines.sort((a, b) => a.top - b.top || a.left - b.left);
}

function titleLine(line: TextLine): boolean {
  return titleKey(line.text).length >= 7 && line.text.length <= 350
    && !/^(?:abstract|introduction|references|contents|article|open access|research article|review|perspective|letter|preprint)$/i.test(line.text)
    && !/^(?:journal of |proceedings of |submitted |received[: ]|accepted[: ]|published[: ]|copyright|©)/i.test(line.text)
    && !/(?:https?:\/\/|www\.|@|\bdoi\s*:)/i.test(line.text);
}

function joinTitle(lines: TextLine[]): string {
  let title = '';
  for (const line of lines) {
    const text = line.text.replace(/\s*[*∗†‡]+\s*$/u, '').trim();
    if (/[\p{L}][-‐\u00ad]$/u.test(title) && /^\p{L}/u.test(text)) {
      title = title.slice(0, -1) + text;
    } else {
      title += (title ? ' ' : '') + text;
    }
  }
  return title.normalize('NFKC').replace(/[\u00ad\u200b]/g, '').replace(/\s+/g, ' ').trim();
}

export function extractPdfTitle(items: TextContent['items'], view: readonly number[], metadataTitles: unknown[]): string | undefined {
  const pageHeight = view[3] - view[1];
  const lines = textLines(items, view);
  const candidates = lines.filter(line => line.top >= pageHeight * 0.035 && line.top <= pageHeight * 0.88 && titleLine(line));
  const starts = candidates.filter(line => line.top <= pageHeight * 0.52);
  if (!starts.length) return undefined;
  const size = Math.max(...starts.map(line => line.size));
  const first = starts.find(line => line.size >= size * 0.96)!;
  const selected = [first];
  // The first line must be near the top; subsequent title lines can extend
  // below that search region, as in magazine layouts with a tall title block.
  for (const line of candidates.filter(line => line.top > first.top)) {
    const last = selected[selected.length - 1];
    if (line.top - last.top > Math.max(line.size, last.size) * 1.9) break;
    if (Math.min(line.right, last.right) <= Math.max(line.left, last.left)) continue;
    if (line.size < size * 0.9 || line.size > size * 1.08) break;
    selected.push(line);
  }
  const title = joinTitle(selected);
  if (title.length > 350) return undefined;
  const metadataTitle = metadataTitles.find((value): value is string => typeof value === 'string' && !!value.trim()
    && !/^(?:untitled|microsoft word|acrobat|latex)\b/i.test(value.trim()) && !/\.(?:dvi|docx?|tex|pdf)\s*$/i.test(value)
    && pdfTitlesMatch(title, value));
  if (metadataTitle) return metadataTitle.trim();

  const bodySizes = lines.filter(line => line.top > selected[selected.length - 1].top && line.text.length > 50)
    .map(line => line.size).sort((a, b) => a - b);
  const bodySize = bodySizes[Math.floor(bodySizes.length / 2)];
  return bodySize && size / bodySize >= 1.08 ? title : undefined;
}
