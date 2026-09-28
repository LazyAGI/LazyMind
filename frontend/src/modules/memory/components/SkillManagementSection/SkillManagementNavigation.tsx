import { ApartmentOutlined, AppstoreOutlined, FileTextOutlined } from '@ant-design/icons';
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
      <span className="memory-skill-resource-rail__label">{t('admin.memorySkillResourcesLabel')}</span>
      {([
        { view: 'installed', label: 'admin.memorySkillViewInstalled', icon: <FileTextOutlined /> },
        { view: 'workflows', label: 'admin.memorySkillViewWorkflows', icon: <ApartmentOutlined /> },
        { view: 'market', label: 'admin.memorySkillViewMarket', icon: <AppstoreOutlined /> },
      ] as const).map(({ view, label, icon }) => (
        <button type="button" key={view} disabled={disabled} className={skillView === view ? 'is-active' : ''}
          aria-current={skillView === view ? 'page' : undefined} onClick={() => onSkillViewChange(view)}>
          <span aria-hidden="true">{icon}</span><span>{t(label)}</span>
        </button>
      ))}
    </nav>
  );
}
