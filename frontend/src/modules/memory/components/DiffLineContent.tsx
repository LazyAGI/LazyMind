import React from 'react';
import type { DiffLine } from '../shared';

/**
 * Renders the text content of a single diff line.
 * When the line has inlineSpans, individual changed characters are highlighted
 * with a deeper background color. Otherwise falls back to plain text.
 */
export function DiffLineContent({ line }: { line: DiffLine }) {
  if (!line.inlineSpans) {
    return <code>{line.text}</code>;
  }

  const highlightClass =
    line.type === 'add' ? 'memory-diff-inline-add' : 'memory-diff-inline-remove';

  return (
    <code>
      {line.inlineSpans.map((span, i) =>
        span.highlight ? (
          <mark key={i} className={highlightClass}>{span.text}</mark>
        ) : (
          <span key={i}>{span.text}</span>
        ),
      )}
    </code>
  );
}
