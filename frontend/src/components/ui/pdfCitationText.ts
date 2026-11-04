/** Keep the hover citation readable without changing the stored bibliography. */
export function formatPdfCitationSummary(rawText: string): string {
  const text = rawText.replace(/\s+/g, " ").trim();
  for (const boundary of text.matchAll(/\.\s+(?=\S)/g)) {
    const period = boundary.index;
    const beforePeriod = text.slice(0, period);
    // Initials inside names, such as "J. R. Smith" or "Quoc V. Le",
    // are not the end of the author list.
    if (/(?:^|\s)\p{L}$/u.test(beforePeriod)) continue;

    const firstAuthor = beforePeriod
      .split(/,\s*|\s+(?:and|&)\s+/i, 1)[0]
      .replace(/\s+et\s+al\s*$/i, "")
      .trim();
    const words = firstAuthor.split(/\s+/);
    if (words.length < 2
      || !/^\p{Lu}/u.test(words[0])
      || !/^\p{Lu}/u.test(words[words.length - 1])
      || !words.every(word => /^[\p{L}.'’\-]+$/u.test(word))) continue;

    const remainder = text.slice(period + 1).trim();
    const hasAuthorList = /,\s*\S|\s+(?:and|&|et\s+al)\b/i.test(beforePeriod);
    // A two-word title can look like a single author. For that ambiguous
    // shape, require a publication year later in the same entry.
    if (!hasAuthorList && !/\b(?:18|19|20)\d{2}[a-z]?\b/i.test(remainder)) continue;

    return `${firstAuthor} et al. ${remainder}`;
  }
  return text;
}
