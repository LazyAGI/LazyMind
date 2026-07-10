import type { SkillDiffEntryLine } from "./skillDiffUtils";
import { DiffLineContent } from "../DiffLineContent";
import { toDiffLine } from "./skillDiffUtils";

export function SkillDiffLineContent({ line }: { line: SkillDiffEntryLine }) {
  if (line.html) {
    return (
      <code
        className="memory-skill-diff-line-html"
        dangerouslySetInnerHTML={{ __html: line.html }}
      />
    );
  }

  return <DiffLineContent line={toDiffLine(line)} />;
}
