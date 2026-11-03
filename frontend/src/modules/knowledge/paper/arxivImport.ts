/** A routing hint only. The backend parses and validates the original input. */
export function isArxivInput(input: string): boolean {
  const value = input.trim();
  return /(?:^|[\s(<])(?:https?:\/\/)?(?:www\.|export\.)?arxiv\.org(?:[/?#]|$)/i.test(value)
    || /(?:^|[\s(<])arxiv\s*:/i.test(value)
    || /(?:^|[\s(/])10\.48550\/arxiv\./i.test(value)
    || /^(?:\d{4}\.\d{4,5}|[a-z][a-z.-]*\/\d{7})(?:v\d+)?(?:\.pdf)?(?:\s+\[[^\]]+\])?$/i.test(value);
}
