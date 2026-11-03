import type { MouseEvent } from 'react';
import './PaperBreadcrumb.scss';

interface Props {
  libraryName: string;
  onKnowledgeClick?: () => void;
  onLibraryClick?: () => void;
  libraryHref?: string;
  paperTitle?: string;
}

export default function PaperBreadcrumb({ libraryName, onKnowledgeClick, onLibraryClick, libraryHref, paperTitle }: Props) {
  const follow = (callback?: () => void) => (event: MouseEvent<HTMLAnchorElement>) => {
    if (!callback || event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
    event.preventDefault();
    callback();
  };
  return <nav className="paper-breadcrumb" aria-label="知识库导航"><ol>
    <li><a href="/lib/knowledge/list" onClick={follow(onKnowledgeClick)}>知识库</a></li>
    <li aria-hidden="true" className="paper-breadcrumb-separator">/</li>
    <li className="paper-breadcrumb-library">{libraryHref
      ? <a href={libraryHref} onClick={follow(onLibraryClick)} title={libraryName}>{libraryName}</a>
      : <h1 aria-current="page" title={libraryName}>{libraryName}</h1>}</li>
    {paperTitle && <><li aria-hidden="true" className="paper-breadcrumb-separator">/</li><li className="paper-breadcrumb-paper" aria-current="page" title={paperTitle}>{paperTitle}</li></>}
  </ol></nav>;
}
