import { useEffect } from 'react';
import ReactDOM from 'react-dom';
import type { SelectionActionAnchor } from './artifactRewriteSelection';
import './ArtifactRewriteSelectionAction.scss';

interface ArtifactRewriteSelectionActionProps {
  anchor: SelectionActionAnchor;
  label: string;
  disabled?: boolean;
  onActivate: () => void;
  onDismiss: () => void;
}

export function ArtifactRewriteSelectionAction({
  anchor,
  label,
  disabled = false,
  onActivate,
  onDismiss,
}: ArtifactRewriteSelectionActionProps) {
  useEffect(() => {
    const dismiss = () => onDismiss();
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') dismiss();
    };
    window.addEventListener('scroll', dismiss, true);
    window.addEventListener('resize', dismiss);
    window.addEventListener('keydown', handleKeyDown);
    return () => {
      window.removeEventListener('scroll', dismiss, true);
      window.removeEventListener('resize', dismiss);
      window.removeEventListener('keydown', handleKeyDown);
    };
  }, [onDismiss]);

  return ReactDOM.createPortal(
    <button
      type='button'
      className={`artifact-rewrite-selection-action artifact-rewrite-selection-action--${anchor.placement}`}
      style={{ top: anchor.top, left: anchor.left }}
      disabled={disabled}
      onMouseDown={(event) => {
        if (!disabled) event.preventDefault();
      }}
      onClick={() => {
        if (disabled) return;
        onActivate();
        onDismiss();
      }}
    >
      {label}
    </button>,
    document.body,
  );
}
