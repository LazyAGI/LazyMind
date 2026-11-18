// A single 24px, rounded-stroke family. Generated text selects a trusted glyph;
// it never becomes SVG markup, and no additional model request is required.
const glyphs = {
  bookmark: "M6 4a1 1 0 0 1 1-1h10a1 1 0 0 1 1 1v17l-6-4-6 4V4Z",
  summary: "M14 3H6a1 1 0 0 0-1 1v16a1 1 0 0 0 1 1h12a1 1 0 0 0 1-1V8l-5-5ZM14 3v5h5M9 12h6M9 16h6",
  bottleneck: "M4 4h16M4 20h16M5 7l5 4v2l-5 4M19 7l-5 4v2l5 4M9 4v2M15 18v2",
  idea: "M9 17c0-2-3-3.1-3-7a6 6 0 0 1 12 0c0 3.9-3 5-3 7M9 17h6M10 21h4M12 13V9m-2 1 2 2 2-2",
  results: "M4 4v16h16M8 16v-4M12 16V8M16 16v-6M20 16V5",
  boundary: "M8 4H5a1 1 0 0 0-1 1v14a1 1 0 0 0 1 1h3M16 4h3a1 1 0 0 1 1 1v14a1 1 0 0 1-1 1h-3M12 7v6M12 17h.01",
  glossary: "M12 5C9 3 5 3 3 4v15c3-1 6-1 9 1 3-2 6-2 9-1V4c-2-1-6-1-9 1Zm0 0v15M6 8h3M6 12h3M15 8h3M15 12h3",
  process: "M4 5h6v6H4ZM14 13h6v6h-6ZM10 8h5a2 2 0 0 1 2 2v3M14 10l3 3 3-3M7 15v4h4",
  parameters: "M5 3v6m0 4v8M12 3v12m0 4v2M19 3v3m0 4v11M3 9h4v4H3ZM10 15h4v4h-4ZM17 6h4v4h-4Z",
  geometry: "M4 19 9 5l11 5-5 9H4Zm0 0 9-7 7-2M13 12l2 7M9 5l4 7",
  attention: "M5 4a2 2 0 1 0 0 4 2 2 0 0 0 0-4ZM5 16a2 2 0 1 0 0 4 2 2 0 0 0 0-4ZM19 4a2 2 0 1 0 0 4 2 2 0 0 0 0-4ZM19 16a2 2 0 1 0 0 4 2 2 0 0 0 0-4ZM7 6h10M7 18h10M7 7l10 10M7 17 17 7",
  layers: "m12 3 9 5-9 5-9-5 9-5ZM3 12l9 5 9-5M3 16l9 5 9-5",
  mask: "M4 4h16v16H4ZM4 9h16M4 14h16M9 4v16M14 4v16M5 5l14 14M15 6h3M16 8h2M17 11h1",
  learning: "M3 5v15h18M6 8c2 0 2 7 5 7s3-4 5-4 2 1 4-3M17 5l3 3-3 3",
  reward: "M5 5h14v14H5ZM8 12l3 3 5-6M9 3v2M15 19v2",
  memory: "M7 6h10v12H7ZM10 9h4v6h-4ZM9 3v3M15 3v3M9 18v3M15 18v3M3 9h4M3 15h4M17 9h4M17 15h4",
  scan: "M4 4h4v4H4ZM16 4h4v4h-4ZM10 16h4v4h-4ZM6 8v4h12V8M12 12v4M10 10l2 2-2 2",
  noise: "M3 8c3-6 5 6 8 0s5 6 10 0M3 16c3-6 5 6 8 0s5 6 10 0",
  compression: "M3 5h5v14H3ZM16 8h5v8h-5ZM10 12h4M12 10l2 2-2 2M5 8h1M5 11h1M5 14h1M5 17h1",
  backward: "M8 5 4 9l4 4M4 9h10a6 6 0 0 1 0 12h-4M10 15h4M10 18h4",
  equation: "M5 3H3v18h2M19 3h2v18h-2M8 8l3 8M11 8l-3 8M14 10h3M14 14h3",
  search: "M10 4a6 6 0 1 0 0 12 6 6 0 0 0 0-12ZM15 15l5 5M7 10h6M10 7v6",
} as const;
type IconKind = keyof typeof glyphs;
export interface SectionIconContext { role: string; title?: string; description?: string; optional?: boolean }
const fixed: Record<string, IconKind> = { overview: "summary", "why-it-matters": "bottleneck", "big-idea": "idea", results: "results", limitations: "boundary", glossary: "glossary" };
// Specific concepts first: "noise prediction loss" teaches optimization rather
// than noise itself; "selective scan and GPU memory" teaches parallel scan.
const topics: [IconKind, RegExp][] = [
  ["backward", /反向传播|重算|重计算|\bbackward\b|recomput/i],
  ["scan", /扫描|并行前缀|\bscan\b|associativ/i],
  ["compression", /压缩|码率|编码代价|compression|\bcoding\b|rate.distortion/i],
  ["mask", /遮罩|掩码|位置编码|位置与|\bmask|position/i],
  ["attention", /注意力|\battention\b/i],
  ["geometry", /子空间|共同方向|相似度|几何|subspace|similarity|geometr/i],
  ["learning", /损失|梯度|训练|优化|推动更新|loss|gradient|train|optimi|update.weight/i],
  ["parameters", /参数|预算|秩|缩放|parameter|budget|\brank\b|scaling/i],
  ["reward", /奖励|偏好|reward|preference/i],
  ["layers", /网络块|架构|网络结构|分层|\bblock\b|architect|\blayers?\b/i],
  ["memory", /内存|显存|存储|记忆|缓存|memory|\bcache\b|\bio\b|\bgpu\b/i],
  ["noise", /噪声|加噪|去噪|扩散|noise|noising|diffusion/i],
  ["equation", /推导|证明|等价|deriv|proof|equival/i],
  ["search", /检索|搜索|search|retriev/i],
];
export function sectionIconKind({role, title = "", description = "", optional}: SectionIconContext): IconKind {
  if (fixed[role]) return fixed[role];
  if (optional) return "bookmark";
  // Titles take precedence; immutable plan context handles generic headings and
  // keeps the icon stable while chapter prose streams in.
  for (const text of [title, role.replaceAll("-", " "), description]) {
    const match = topics.find(([, pattern]) => pattern.test(text));
    if (match) return match[0];
  }
  return "process";
}
export default function GuideSectionIcon(props: SectionIconContext) {
  const kind = sectionIconKind(props);
  return <span className="guide-section-icon" data-icon={kind} aria-hidden="true">
    <svg viewBox="0 0 24 24" width="22" height="22" fill="none" stroke="currentColor" strokeWidth="1.65" strokeLinecap="round" strokeLinejoin="round" focusable="false"><path d={glyphs[kind]} /></svg>
  </span>;
}
