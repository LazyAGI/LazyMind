import type { ReactNode } from 'react';

// Local vector assets: one stroke weight and grid for category and skill families.
const file = <path d="M14 3H6a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V9l-6-6v6h6" />;
const book = <path d="M12 6C9 3 5 3 3 4v15c3-1 6 0 9 2 3-2 6-3 9-2V4c-2-1-6-1-9 2v15" />;
const screen = <><rect x="3" y="4" width="18" height="15" rx="2" /><path d="M3 8h18" /></>;
const notebook = <><rect x="6" y="3" width="14" height="18" rx="2" /><path d="M3 7h5M3 12h5M3 17h5" /></>;
const brain = <path d="M12 5C10 1 5 3 5 7c-4 1-4 7 0 8-1 5 4 8 7 4 3 4 8 1 7-4 4-1 4-7 0-8 0-4-5-6-7-2v14" />;
const shapes = {
  // Approved Lucide source icons (ISC); see modules/workflow/components/WorkflowIcon/LICENSE.
  'badge-check': <><path d="M3.85 8.62a4 4 0 0 1 4.78-4.77 4 4 0 0 1 6.74 0 4 4 0 0 1 4.78 4.78 4 4 0 0 1 0 6.74 4 4 0 0 1-4.77 4.78 4 4 0 0 1-6.75 0 4 4 0 0 1-4.78-4.77 4 4 0 0 1 0-6.76Z" /><path d="m9 12 2 2 4-4" /></>,
  'sprout': <><path d="M14 9.536V7a4 4 0 0 1 4-4h1.5a.5.5 0 0 1 .5.5V5a4 4 0 0 1-4 4 4 4 0 0 0-4 4c0 2 1 3 1 5a5 5 0 0 1-1 3" /><path d="M4 9a5 5 0 0 1 8 4 5 5 0 0 1-8-4" /><path d="M5 21h14" /></>,
  'package-open': <><path d="M12 22v-9" /><path d="M15.17 2.21a1.67 1.67 0 0 1 1.63 0L21 4.57a1.93 1.93 0 0 1 0 3.36L8.82 14.79a1.655 1.655 0 0 1-1.64 0L3 12.43a1.93 1.93 0 0 1 0-3.36z" /><path d="M20 13v3.87a2.06 2.06 0 0 1-1.11 1.83l-6 3.08a1.93 1.93 0 0 1-1.78 0l-6-3.08A2.06 2.06 0 0 1 4 16.87V13" /><path d="M21 12.43a1.93 1.93 0 0 0 0-3.36L8.83 2.2a1.64 1.64 0 0 0-1.63 0L3 4.57a1.93 1.93 0 0 0 0 3.36l12.18 6.86a1.636 1.636 0 0 0 1.63 0z" /></>,
  'ability-badge': <><path d="m12 2 9 5v10l-9 5-9-5V7z" /><path d="m13 6-6 7h4l-1 5 7-8h-4z" /></>,
  'scan-search': <><path d="M8 3H5a2 2 0 0 0-2 2v3m13-5h3a2 2 0 0 1 2 2v3M3 16v3a2 2 0 0 0 2 2h3m13-5v3a2 2 0 0 1-2 2h-3" /><circle cx="11" cy="11" r="4" /><path d="m14 14 3 3" /></>,
  'pen-line': <><path d="m4 16-1 5 5-1L20 8a3 3 0 0 0-4-4zm10-10 4 4M13 21h8" /></>,
  'code-window': <>{screen}<path d="m9 11-3 3 3 3m6-6 3 3-3 3m-2-6-2 6" /></>,
  'palette': <><path d="M12 3a9 9 0 1 0 0 18c3 0 4-2 2-4-1-1 0-3 2-3h2c5 0 4-11-6-11Z" /><circle cx="7" cy="9" r=".8" /><circle cx="11" cy="6.5" r=".8" /><circle cx="16" cy="8" r=".8" /></>,
  'chart-table': <><rect x="3" y="3" width="18" height="18" rx="2" /><path d="M3 8h18M8 8v13m3-4v-3m4 3v-6m3 6v-4" /></>,
  'bot': <><rect x="4" y="7" width="16" height="13" rx="3" /><path d="M12 7V3m-2 0h4M1 12v4m22-4v4M9 16h6M8 11v1m8-1v1" /></>,
  'briefcase': <><rect x="3" y="7" width="18" height="14" rx="2" /><path d="M8 7V3h8v4M3 12c6 4 12 4 18 0m-9 1v4" /></>,
  'open-book': book,
  'files': <><path d="M8 3h8l4 4v9a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2Zm6 0v5h6M3 8v12a2 2 0 0 0 2 2h10" /></>,
  'orbit-search': <><circle cx="11" cy="11" r="4" /><path d="m14 14 5 5M5 3a10 10 0 0 0-2 13M17 3a10 10 0 0 1 4 9" /><circle cx="4" cy="19" r="1" /><circle cx="12" cy="2" r="1" /></>,
  'file-check': <>{file}<path d="m8 15 3 3 5-6" /></>,
  'book-check': <>{book}<path d="m15 11 2 2 3-4M6 8h3M6 12h3" /></>,
  'file-search': <>{file}<circle cx="11" cy="14" r="3" /><path d="m13 16 3 3" /></>,
  'telescope': <><path d="m3 9 15-6 3 7-16 5zm12-5 3 7M10 14l-3 7m6-8 4 8M2 10l2 5" /></>,
  'globe-search': <><circle cx="10" cy="10" r="7" /><path d="M3 10h14M10 3c-4 4-4 10 0 14 4-4 4-10 0-14m5 12 6 6" /></>,
  'terminal': <><rect x="3" y="4" width="18" height="16" rx="2" /><path d="m7 9 4 3-4 3m6 1h4" /></>,
  'cloud-download': <><path d="M7 17H5a4 4 0 0 1-1-8 7 7 0 0 1 13-2 5 5 0 0 1 2 10h-2M12 11v10m-4-4 4 4 4-4" /></>,
  'compass': <><circle cx="12" cy="12" r="9" /><path d="m16 8-3 5-5 3 3-5z" /></>,
  'decision-board': <><rect x="3" y="3" width="18" height="18" rx="2" /><path d="M3 8h18M8 8v13m3-9 2 2 4-3m-6 6h6" /></>,
  'feather': <><path d="M5 19v-7l9-9c6-3 10 3 6 7l-9 9ZM3 22 16 8m-7 7h7" /></>,
  'notebook-pen': <>{notebook}<path d="m11 15 1-4 5-5 2 2-5 5zM11 18h5" /></>,
  'languages': <><path d="M2 5h12M8 2v3m3 0c0 5-4 9-8 11M5 8c1 3 4 5 7 6m1 7 4-11 4 11m-7-3h6" /></>,
  'stamp': <><path d="M4 21h16M4 18v-4h5V9a4 4 0 1 1 6 0v5h5v4z" /></>,
  'file-print': <>{file}<path d="M8 13h8M8 17h8M8 10h2" /></>,
  'file-edit': <>{file}<path d="m8 18 1-4 5-5 3 3-5 5z" /></>,
  'notebook-text': <>{notebook}<path d="M11 8h5m-5 4h5m-5 4h4" /></>,
  'book-star': <>{book}<path d="m16 8 1 2 2 .5-1.5 1.5.5 2-2-1-2 1 .5-2-1.5-1.5 2-.5z" /></>,
  'type-lines': <><path d="M3 4h12m-6 0v13m-4 0h8M3 4v3m12-3v3m3 4h3m-5 5h5M3 21h18" /></>,
  'code-file': <>{file}<path d="m9 12-3 3 3 3m6-6 3 3-3 3" /></>,
  'key-terminal': <>{screen}<circle cx="8" cy="13" r="2" /><path d="M10 13h7m-2 0v3m-2-3v2" /></>,
  'chip': <><rect x="6" y="6" width="12" height="12" rx="2" /><rect x="9" y="9" width="6" height="6" rx="1" /><path d="M9 3v3m6-3v3M9 18v3m6-3v3M3 9h3m-3 6h3m12-6h3m-3 6h3" /></>,
  'api': <><path d="m6 5-4 7 4 7m12-14 4 7-4 7M8 10h8m-6 4h4" /><circle cx="12" cy="12" r="5" /></>,
  'code-check': <><path d="m8 4-5 5 5 5m6-10 5 5-5 5m-6 4 4 3 7-7" /></>,
  'git-branch': <><circle cx="6" cy="4" r="2" /><circle cx="18" cy="5" r="2" /><circle cx="6" cy="20" r="2" /><path d="M6 6v12M18 7c0 6-12 2-12 9" /></>,
  'review-chat': <><path d="M5 3h14a2 2 0 0 1 2 2v10a2 2 0 0 1-2 2H9l-6 4V5a2 2 0 0 1 2-2Z" /><path d="m8 10 3 3 5-6" /></>,
  'shield-check': <><path d="m12 3 8 3v6c0 4-3 7-8 10-5-3-8-6-8-10V6z" /><path d="m8 12 3 3 5-6" /></>,
  'test-tube': <><path d="M8 3h10m-8 0v7L4 16a4 4 0 0 0 6 5l6-7V3M8 12l5 5" /></>,
  'scissors': <><circle cx="6" cy="6" r="3" /><circle cx="6" cy="18" r="3" /><path d="m9 8 12 13M9 16 21 3" /></>,
  'layers': <><path d="m12 2 10 6-10 6L2 8zm-9 11 9 5 9-5m-18 5 9 5 9-5" /></>,
  'bolt-code': <><path d="m6 7-4 5 4 5m12-10 4 5-4 5m-5-14-5 10h5l-2 8 5-11h-5z" /></>,
  'video': <><rect x="3" y="3" width="18" height="18" rx="2" /><path d="M7 3v18M17 3v18M3 8h4m-4 8h4m10-8h4m-4 8h4m-10-5 4 3-4 3z" /></>,
  'cover-image': <><rect x="3" y="3" width="18" height="18" rx="2" /><circle cx="8" cy="8" r="2" /><path d="m3 18 6-6 4 4 4-7 4 5" /></>,
  'animation': <><path d="M6 3H3v18h3m2-2h4V5H8" /><rect x="14" y="7" width="7" height="10" rx="1" /></>,
  'layout': <>{screen}<path d="M9 8v11m3-8h6m-6 4h4" /></>,
  'swatches': <><rect x="3" y="3" width="6" height="18" rx="2" /><path d="m9 6 3-3 5 5-8 8m0-5h12v10H6M6 17h.01" /></>,
  'file-link': <>{file}<path d="m8 13 2-2a2 2 0 0 1 3 3l-2 2m-1-3-2 2a2 2 0 0 0 3 3l2-2" /></>,
  'spreadsheet': <><rect x="3" y="3" width="18" height="18" rx="2" /><path d="M3 9h18M3 15h18M9 3v18m6-12v12" /></>,
  'folder-sort': <><path d="M3 7V5a2 2 0 0 1 2-2h5l3 4h6a2 2 0 0 1 2 2v10a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2Z" /><path d="M8 11v6m-2-2 2 2 2-2m3-4h4m-4 4h3" /></>,
  'chart-line': <><path d="M3 3v18h18M6 16l4-6 5 3 6-9" /></>,
  'brain-cycle': <>{brain}<path d="m7 10 2-2 2 2m6 4-2 2-2-2" /></>,
  'agent-nodes': <><circle cx="12" cy="10" r="3" /><circle cx="4" cy="20" r="2" /><circle cx="20" cy="20" r="2" /><circle cx="12" cy="3" r="1" /><path d="M12 7V4m-2 8-5 6m9-6 5 6M8 19h8" /></>,
  'browser-pointer': <>{screen}<path d="m12 11 1 10 3-3 4-1z" /></>,
  'brain': brain,
  'task-tree': <><rect x="9" y="2" width="6" height="5" rx="1" /><path d="M12 7v5H4v4m8-4h8v4" /><path d="m1 19 2 2 4-4m10 2 2 2 4-4" /></>,
  'wrench': <><path d="M14 3a6 6 0 0 0-6 8L2 17a3 3 0 0 0 5 5l7-8a6 6 0 0 0 7-8l-5 4-3-3z" /></>,
  'prompt': <><path d="M5 3h14a2 2 0 0 1 2 2v12H9l-6 4V5a2 2 0 0 1 2-2Z" /><path d="m7 7 3 3-3 3m6 0h4" /></>,
  'target': <><circle cx="12" cy="12" r="9" /><circle cx="12" cy="12" r="5" /><circle cx="12" cy="12" r="1" /></>,
  'calendar': <><rect x="3" y="5" width="18" height="16" rx="2" /><path d="M7 2v6m10-6v6M3 10h18M7 14h3m4 0h3m-10 4h3" /></>,
  'radar': <><circle cx="12" cy="12" r="9" /><circle cx="12" cy="12" r="5" /><path d="M12 12 19 5" /><circle cx="8" cy="14" r="1" /></>,
  'coins': <><ellipse cx="10" cy="6" rx="7" ry="3" /><path d="M3 6v6c0 4 14 4 14 0V6M3 9c0 4 14 4 14 0m0 1c6 0 6 5 0 5m-5 1v3c0 4 10 4 10 0v-6" /></>,
  'social': <><circle cx="5" cy="12" r="3" /><circle cx="19" cy="5" r="3" /><circle cx="19" cy="19" r="3" /><path d="m8 11 8-5m-8 7 8 5" /></>,
  'building': <><path d="M5 22V3h12v19M2 22h20m-5-12h4v12M9 7h4M9 11h4m-4 4h4m-4 7v-3h4v3" /></>,
  'calculator': <><rect x="5" y="2" width="14" height="20" rx="2" /><path d="M8 6h8v4H8zm0 8h1m3 0h1m3 0h.01M8 18h1m3 0h1m3 0h.01" /></>,
  'compare': <><path d="M12 2v20M3 21V9h5v12m8 0V4h5v17M2 21h20" /></>,
  'profile-chart': <><circle cx="8" cy="6" r="3" /><path d="M2 18v-2a6 6 0 0 1 10-4m3 9v-6m3 6V9m3 12V4" /></>,
  'megaphone': <><path d="M3 9h5l11-6v18L8 15H3zm5 6 2 7H6l-2-7m15-6 3 1v4l-3 1" /></>,
  'flame': <path d="M12 2c2 5-4 6-3 11 2 0 4-2 5-5 8 7 7 14-2 14S1 13 6 8c0 3 1 4 2 4-2-6 1-7 4-10Z" />,
  'chat-search': <><path d="M21 12V5a2 2 0 0 0-2-2H5a2 2 0 0 0-2 2v16l5-4h4" /><circle cx="15" cy="14" r="4" /><path d="m18 17 4 4" /></>,
  'mail': <><rect x="2" y="4" width="20" height="16" rx="2" /><path d="m2 6 10 7L22 6" /></>,
  'cards': <><path d="M4 5V3h14v2M2 9V7h18v2" /><rect x="5" y="11" width="17" height="11" rx="2" /><path d="M9 15h5m-5 3h8" /></>,
  'lightbulb': <><path d="M8 17c0-3-4-4-4-8a8 8 0 0 1 16 0c0 4-4 5-4 8zm1 4h6m-3-4v-6m-3-3 3 3 3-3" /></>,
  'graduation': <><path d="m2 8 10-5 10 5-10 5zM6 10v7c4 3 8 3 12 0v-7m4-2v8" /></>,
  'dialogue': <><path d="M3 3h14v11H8l-5 4zm7 15h6l5 4V9h-1" /><path d="M7 7h6m-6 3h4" /></>,
} satisfies Record<string, ReactNode>;

export type SkillIconName = keyof typeof shapes;
export default function SkillIcon({ name, size = '1em', className }: { name: SkillIconName; size?: number | string; className?: string }) {
  return <svg className={className} width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor"
    strokeWidth={1.7} strokeLinecap="round" strokeLinejoin="round" aria-hidden="true" focusable="false"
    data-skill-icon={name} style={{ display: 'inline-block', verticalAlign: '-0.125em', flexShrink: 0 }}>
    {shapes[name]}
  </svg>;
}
