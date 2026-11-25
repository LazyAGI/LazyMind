type ToolbarIconName = 'graph' | 'approval' | 'following' | 'browse' | 'chevron' | 'external-link';

/** Consistent, rounded outline icons for the compact workflow toolbar. */
export function WorkflowToolbarIcon({ name }: { name: ToolbarIconName }) {
  return (
    <svg className={`workflow-panel__toolbar-icon${name === 'chevron' ? ' workflow-panel__toolbar-icon--chevron' : ''}`}
      width='18' height='18' viewBox='0 0 24 24' fill='none' stroke='currentColor'
      strokeWidth={name === 'external-link' ? 1.6 : 1.8} strokeLinecap='round' strokeLinejoin='round' aria-hidden='true' focusable='false'>
      {name === 'external-link' && <>
        <path d='M15 3h6v6M10 14 21 3' />
        <path d='M21 14v5a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h6' />
      </>}
      {name === 'graph' && <>
        <path d='M6 3v12M18 9a9 9 0 0 1-9 9' />
        <circle cx='6' cy='18' r='3' />
        <circle cx='18' cy='6' r='3' />
      </>}
      {name === 'approval' && <>
        <path d='M12 22s8-4 8-11V5c-3 0-5-1-8-3-3 2-5 3-8 3v6c0 7 8 11 8 11Z' />
        <path d='m9 12 2 2 4-4' />
      </>}
      {name === 'following' && <>
        <circle cx='12' cy='12' r='8' />
        <circle cx='12' cy='12' r='3' />
        <path d='M12 2v2m0 16v2M2 12h2m16 0h2' />
      </>}
      {name === 'browse' && <path d='m4 4 7.1 17 2.5-7.4 7.4-2.5L4 4Z' />}
      {name === 'chevron' && <path d='m6 9 6 6 6-6' />}
    </svg>
  );
}
