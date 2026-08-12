function backtickRunLength(value: string, start: number): number {
  let end = start;
  while (value[end] === '`') end += 1;
  return end - start;
}

function isEscaped(value: string, index: number): boolean {
  let backslashes = 0;
  for (let cursor = index - 1; cursor >= 0 && value[cursor] === '\\'; cursor -= 1) {
    backslashes += 1;
  }
  return backslashes % 2 === 1;
}

function escapeMdxLessThanInLine(line: string): string {
  let result = '';
  let inlineCodeFence = 0;

  for (let index = 0; index < line.length;) {
    if (line[index] === '`') {
      const runLength = backtickRunLength(line, index);
      if (inlineCodeFence === 0) {
        inlineCodeFence = runLength;
      } else if (inlineCodeFence === runLength) {
        inlineCodeFence = 0;
      }
      result += line.slice(index, index + runLength);
      index += runLength;
      continue;
    }

    if (line[index] === '<' && inlineCodeFence === 0 && !isEscaped(line, index)) {
      const next = line[index + 1] ?? '';
      // MDX treats "<" as a JSX tag opener. Keep valid HTML/JSX openers,
      // comments and fragments intact; escape comparison/plain-text uses.
      if (!/[A-Za-z_$/>!?]/.test(next)) {
        result += '\\';
      }
    }

    result += line[index];
    index += 1;
  }

  return result;
}

/**
 * Makes plain Markdown safe for MDXEditor without changing code examples.
 * MDX parses text such as "<5%" or "<= 10" as malformed JSX.
 */
export function normalizeMarkdownForMdxEditor(markdown: string): string {
  let fenceCharacter = '';
  let fenceLength = 0;

  return markdown.split('\n').map((line) => {
    const fence = line.match(/^ {0,3}(`{3,}|~{3,})/);
    if (fence) {
      const marker = fence[1];
      if (!fenceCharacter) {
        fenceCharacter = marker[0];
        fenceLength = marker.length;
      } else if (marker[0] === fenceCharacter && marker.length >= fenceLength) {
        fenceCharacter = '';
        fenceLength = 0;
      }
      return line;
    }
    return fenceCharacter ? line : escapeMdxLessThanInLine(line);
  }).join('\n');
}
