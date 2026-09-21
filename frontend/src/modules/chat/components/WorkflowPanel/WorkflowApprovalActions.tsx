import { useTranslation } from 'react-i18next';

export type WorkflowApprovalScope = 'step' | 'following';

// Hosts share approval choices; each adapter owns persistence and continuation.
export function WorkflowApprovalActions({ disabled, onContinue }: {
  disabled: boolean;
  onContinue(scope?: WorkflowApprovalScope): void;
}) {
  const { t } = useTranslation();
  return (
      <div className='workflow-panel__approval-bar' role='group' aria-label={t('chat.workflowApprovalActions')}>
        <span className='workflow-panel__approval-label'>{t('chat.workflowApprovalRequired')}</span>
        <button
          type='button'
          className='workflow-panel__action-btn workflow-panel__action-btn--primary'
          disabled={disabled}
          aria-disabled={disabled}
          onClick={() => onContinue()}
        >
          {t('chat.workflowContinueExecution')}
        </button>
        <button
          type='button'
          className='workflow-panel__action-btn workflow-panel__action-btn--secondary'
          disabled={disabled}
          aria-disabled={disabled}
          onClick={() => onContinue('step')}
          title={t('chat.workflowSkipThisApprovalHint')}
        >
          {t('chat.workflowSkipThisApproval')}
        </button>
        <button
          type='button'
          className='workflow-panel__action-btn workflow-panel__action-btn--secondary'
          disabled={disabled}
          aria-disabled={disabled}
          onClick={() => onContinue('following')}
          title={t('chat.workflowSkipFollowingApprovalsHint')}
        >
          {t('chat.workflowSkipFollowingApprovals')}
        </button>
      </div>
  );
}
