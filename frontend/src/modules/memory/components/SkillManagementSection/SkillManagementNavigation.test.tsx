import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import SkillManagementNavigation from './SkillManagementNavigation';

describe('SkillManagementNavigation', () => {
  it('exposes only the three resource destinations', () => {
    const onSkillViewChange = vi.fn();
    render(<SkillManagementNavigation t={(key) => key} skillView="installed" onSkillViewChange={onSkillViewChange} />);
    expect(screen.getAllByRole('button')).toHaveLength(3);
    expect(screen.getByRole('button', { name: 'admin.memorySkillViewInstalled' })).toHaveAttribute('aria-current', 'page');
    fireEvent.click(screen.getByRole('button', { name: 'admin.memorySkillViewWorkflows' }));
    expect(onSkillViewChange).toHaveBeenCalledWith('workflows');
    fireEvent.click(screen.getByRole('button', { name: 'admin.memorySkillViewMarket' }));
    expect(onSkillViewChange).toHaveBeenCalledWith('market');
  });
});
