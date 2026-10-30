import './PaperAuthors.scss';

/** Compact display only: the complete author list remains available to export/edit. */
export default function PaperAuthors({ authors, empty = '作者未记录' }: { authors: readonly string[]; empty?: string }) {
  const names = authors.map(name => name.trim()).filter(Boolean);
  const full = names.join(', ');
  return <span className="paper-authors" title={full || empty} aria-label={full || empty}>
    <span className="paper-authors-leading">{(names.length > 3 ? names.slice(0, 2) : names).join(', ') || empty}</span>
    {names.length > 3 && <><span className="paper-authors-omission" aria-hidden="true" title={`省略中间 ${names.length - 3} 位作者`}>···</span><span className="paper-authors-last">{names[names.length - 1]}</span></>}
  </span>;
}
