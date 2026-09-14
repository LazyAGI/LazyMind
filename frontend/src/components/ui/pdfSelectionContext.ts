export function extractPdfSelectionContext(pageText: string, selectedText: string) {
  const source = pageText.trim();
  const selected = selectedText.trim();
  if (!source || !selected) return selected;
  const index = source.indexOf(selected);
  if (index < 0) return selected;
  const endIndex = index + selected.length;
  const boundary = /[。！？.!?\n]/;
  let start = index;
  while (start > 0 && !boundary.test(source[start - 1])) start -= 1;
  let end = endIndex;
  while (end < source.length && !boundary.test(source[end])) end += 1;
  if (end < source.length) end += 1;
  const paragraph = source.slice(start, end).trim();
  if (!paragraph) return selected;
  if (paragraph.length <= 1200) return paragraph;
  const windowStart = Math.max(0, index - 400);
  return source.slice(windowStart, Math.min(source.length, endIndex + 800)).trim();
}
