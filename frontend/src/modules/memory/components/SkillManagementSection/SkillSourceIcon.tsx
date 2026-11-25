import type { StructuredAsset } from '../../shared';
import { resolveSkillOriginKind, type OriginKind } from './OriginMark';
import SkillIcon, { type SkillIconName } from './SkillIcon';

const sourceIcons: Partial<Record<OriginKind, SkillIconName>> = {
  builtin: 'badge-check',
  internal: 'sprout',
  external: 'package-open',
};

export default function SkillSourceIcon({ skill }: { skill: StructuredAsset }) {
  // Use the source label's rules, including builtin priority and URL/upload imports.
  // Cloud and unknown sources retain their existing generic skill icon.
  const name = sourceIcons[resolveSkillOriginKind(skill)] || 'ability-badge';
  return <SkillIcon name={name} size={18} />;
}
