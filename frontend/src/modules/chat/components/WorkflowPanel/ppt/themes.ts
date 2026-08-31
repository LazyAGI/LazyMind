import type { ThemeId } from './slideSchema';

export interface ResolvedTheme {
  id: ThemeId;
  name: string;
  bg: string;
  bgAlt: string;
  primary: string;
  accent: string;
  text: string;
  muted: string;
  cardBg: string;
  cardBorder: string;
  /** Soft decorative wash for preview (CSS). */
  wash: string;
}

const THEMES: Record<ThemeId, ResolvedTheme> = {
  corporate_blue: {
    id: 'corporate_blue',
    name: 'Corporate Blue',
    bg: '#0B1F3A',
    bgAlt: '#132C4F',
    primary: '#3B82F6',
    accent: '#60A5FA',
    text: '#F8FAFC',
    muted: '#CBD5E1',
    cardBg: 'rgba(255,255,255,0.08)',
    cardBorder: 'rgba(96,165,250,0.45)',
    wash: 'radial-gradient(ellipse at 20% 0%, rgba(59,130,246,0.35), transparent 55%)',
  },
  festival_red: {
    id: 'festival_red',
    name: 'Festival Red',
    bg: '#8B0000',
    bgAlt: '#A61B1B',
    primary: '#FFD700',
    accent: '#FFC857',
    text: '#FFF8E7',
    muted: '#FFE4B5',
    cardBg: 'rgba(0,0,0,0.22)',
    cardBorder: 'rgba(255,215,0,0.55)',
    wash: 'radial-gradient(ellipse at 50% 0%, rgba(255,215,0,0.22), transparent 60%)',
  },
  ink_wash: {
    id: 'ink_wash',
    name: 'Ink Wash',
    bg: '#F4EFE3',
    bgAlt: '#E8E0D0',
    primary: '#1F1C18',
    accent: '#B42222',
    text: '#1F1C18',
    muted: '#5C564C',
    cardBg: 'rgba(255,255,255,0.55)',
    cardBorder: 'rgba(31,28,24,0.18)',
    wash: 'radial-gradient(ellipse at 80% 20%, rgba(200,193,174,0.55), transparent 50%)',
  },
  dark_tech: {
    id: 'dark_tech',
    name: 'Dark Tech',
    bg: '#050711',
    bgAlt: '#0E1224',
    primary: '#00E5FF',
    accent: '#FF2BD6',
    text: '#E8F7FF',
    muted: '#9FB3C8',
    cardBg: 'rgba(0,229,255,0.06)',
    cardBorder: 'rgba(0,229,255,0.35)',
    wash: 'linear-gradient(135deg, rgba(0,229,255,0.12), transparent 40%, rgba(255,43,214,0.1))',
  },
  fresh_green: {
    id: 'fresh_green',
    name: 'Fresh Green',
    bg: '#F3FBF6',
    bgAlt: '#E4F5EB',
    primary: '#0F766E',
    accent: '#14B8A6',
    text: '#134E4A',
    muted: '#3F6F68',
    cardBg: '#FFFFFF',
    cardBorder: 'rgba(15,118,110,0.25)',
    wash: 'radial-gradient(ellipse at 10% 90%, rgba(20,184,166,0.18), transparent 50%)',
  },
  warm_sand: {
    id: 'warm_sand',
    name: 'Warm Sand',
    bg: '#2A2118',
    bgAlt: '#3A2E22',
    primary: '#E8C39E',
    accent: '#D97706',
    text: '#F8F1E7',
    muted: '#D6C3AE',
    cardBg: 'rgba(255,255,255,0.07)',
    cardBorder: 'rgba(232,195,158,0.35)',
    wash: 'radial-gradient(ellipse at 70% 10%, rgba(217,119,6,0.25), transparent 55%)',
  },
};

const ALIAS: Record<string, ThemeId> = {
  blue: 'corporate_blue',
  corporate: 'corporate_blue',
  red: 'festival_red',
  festival: 'festival_red',
  spring: 'festival_red',
  ink: 'ink_wash',
  wash: 'ink_wash',
  水墨: 'ink_wash',
  cyber: 'dark_tech',
  cyberpunk: 'dark_tech',
  tech: 'dark_tech',
  green: 'fresh_green',
  fresh: 'fresh_green',
  sand: 'warm_sand',
  warm: 'warm_sand',
};

export function resolveTheme(theme?: string): ResolvedTheme {
  const key = (theme || 'corporate_blue').trim().toLowerCase().replace(/\s+/g, '_');
  if (key in THEMES) return THEMES[key as ThemeId];
  if (key in ALIAS) return THEMES[ALIAS[key]];
  return THEMES.corporate_blue;
}

export const SAFE_FONT_STACK = '"Microsoft YaHei", "PingFang SC", "Noto Sans SC", Arial, sans-serif';
export const PPT_FONT_FACE = 'Microsoft YaHei';
