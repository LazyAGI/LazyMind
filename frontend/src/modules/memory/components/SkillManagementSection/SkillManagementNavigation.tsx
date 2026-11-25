import WorkflowIcon from '@/modules/workflow/components/WorkflowIcon';
import type { SkillViewMode } from '../../shared';

interface SkillManagementNavigationProps {
  t: (key: string, options?: Record<string, unknown>) => string;
  skillView: SkillViewMode | 'workflows';
  disabled?: boolean;
  onSkillViewChange: (view: SkillViewMode | 'workflows') => void;
}

export default function SkillManagementNavigation({ t, skillView, onSkillViewChange, disabled = false }: SkillManagementNavigationProps) {
  return (
    <nav className="memory-skill-resource-rail" aria-label={t('admin.memorySkillViewBarLabel')}>
      {([
        { view: 'installed', label: 'admin.memorySkillViewInstalled', icon: <WorkflowIcon name="puzzle" size={18} /> },
        { view: 'workflows', label: 'admin.memorySkillViewWorkflows', icon: <WorkflowIcon name="workflow" size={18} /> },
      ] as const).map(({ view, label, icon }) => (
        <button type="button" key={view} disabled={disabled} className={skillView === view ? 'is-active' : ''}
          aria-current={skillView === view ? 'page' : undefined} onClick={() => onSkillViewChange(view)}>
          <span aria-hidden="true">{icon}</span><span>{t(label)}</span>
        </button>
      ))}
      <span className="memory-skill-resource-rail__divider" aria-hidden="true" />
      <button type="button" disabled={disabled} className={skillView === 'market' ? 'is-active' : ''}
        aria-current={skillView === 'market' ? 'page' : undefined} onClick={() => onSkillViewChange('market')}>
        <span aria-hidden="true"><WorkflowIcon name="store" size={18} /></span><span>{t('admin.memorySkillViewMarket')}</span>
      </button>
    </nav>
  );
}
