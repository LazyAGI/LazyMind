import { describe, expect, it } from 'vitest';
import { resolveRuntimeFeatures } from '../../frontend/src/runtime/features.ts';
import { shouldShowSkillMessageCenter } from '../../frontend/src/modules/memory/components/SkillManagementSection/collaborationVisibility.ts';

describe('skill collaboration surface visibility', () => {
  it('keeps the message center visible in cloud mode', () => {
    const features = resolveRuntimeFeatures({ VITE_LAZYMIND_MODE: 'cloud' });

    expect(
      shouldShowSkillMessageCenter({
        skillView: 'installed',
        hideUserGroupSurfaces: features.hideUserGroupSurfaces,
      }),
    ).toBe(true);
  });

  it.each(['local', 'desktop'])('hides the message center in %s mode', (mode) => {
    const features = resolveRuntimeFeatures({ VITE_LAZYMIND_MODE: mode });

    expect(
      shouldShowSkillMessageCenter({
        skillView: 'installed',
        hideUserGroupSurfaces: features.hideUserGroupSurfaces,
      }),
    ).toBe(false);
  });

  it('does not show the message center outside the installed view', () => {
    expect(
      shouldShowSkillMessageCenter({
        skillView: 'plugins',
        hideUserGroupSurfaces: false,
      }),
    ).toBe(false);
  });
});
