// Only explicit CSS color values are recognized; words such as "red" may be
// ordinary prose. Do not interpret URLs, arbitrary CSS or incomplete hex values.
export function parseMarkdownColor(value: string): { hex: string; alpha: number } | undefined {
  if (value !== value.trim()) return undefined;
  if (/^#(?:[\da-f]{3,4}|[\da-f]{6}|[\da-f]{8})$/i.test(value)) {
    const hex = value.slice(1);
    const expanded = hex.length <= 4 ? Array.from(hex, char => char + char).join('') : hex;
    return { hex: `#${expanded.slice(0, 6)}`, alpha: expanded.length === 8 ? parseInt(expanded.slice(6), 16) / 255 : 1 };
  }
  const rgb = /^rgba?\(\s*(\d{1,3})\s*,\s*(\d{1,3})\s*,\s*(\d{1,3})(?:\s*,\s*(0?(?:\.\d+)|0|1(?:\.0+)?))?\s*\)$/i.exec(value);
  if (!rgb || rgb.slice(1, 4).some(channel => Number(channel) > 255)
    || (value.slice(0, 4).toLowerCase() === 'rgba') !== (rgb[4] !== undefined)) return undefined;
  return { hex: `#${rgb.slice(1, 4).map(channel => Number(channel).toString(16).padStart(2, '0')).join('')}`, alpha: Number(rgb[4] ?? 1) };
}

export function markdownColorMatches(text: string) {
  return Array.from(text.matchAll(/#[\da-f]+\b|rgba?\([^()\n]*\)/gi)).filter(match =>
    !/[\w/#-]/.test(text[match.index! - 1] ?? '') && parseMarkdownColor(match[0]));
}

export function replaceMarkdownColor(original: string, hex: string): string {
  if (!/^#[\da-f]{6}$/i.test(hex)) return original;
  if (original.startsWith('#')) {
    const alpha = original.length === 5 ? original[4].repeat(2) : original.length === 9 ? original.slice(7) : '';
    const value = hex + alpha;
    return /[A-F]/.test(original) ? value.toUpperCase() : value.toLowerCase();
  }
  const channels = [1, 3, 5].map(index => parseInt(hex.slice(index, index + 2), 16));
  let index = 0;
  // Preserve the spelling, spacing and opacity of the original RGB(A) value.
  return original.replace(/\d+/g, value => index < 3 ? String(channels[index++]) : value);
}
