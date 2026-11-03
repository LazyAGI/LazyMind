// A PDF text layer inserts visual line wraps into the selection. Keep real
// paragraph/list boundaries, but join the lines belonging to the same paragraph.
export function normalizePdfSelectionText(value: string): string {
  const lines = value.replace(/\r\n?/g, "\n").replace(/\u00a0/g, " ").split("\n");
  let result = "";
  let paragraphBreak = false;
  let softHyphen = false;
  for (const raw of lines) {
    const line = raw.replace(/\u00ad/g, "").replace(/[\t ]+/g, " ").trim();
    if (!line) { paragraphBreak = Boolean(result); continue; }
    const isList = /^(?:[•●▪◦*-]\s|\d+[.)]\s)/u.test(line);
    const joinsChinese = /[\p{Script=Han}，。！？；：、]$/u.test(result) && /^[\p{Script=Han}]/u.test(line);
    const separator = !result ? "" : paragraphBreak ? "\n\n" : isList ? "\n"
      : softHyphen || /[-‐]$/u.test(result) || joinsChinese ? "" : " ";
    result += separator + line;
    softHyphen = /\u00ad\s*$/u.test(raw);
    paragraphBreak = false;
  }
  return result;
}
