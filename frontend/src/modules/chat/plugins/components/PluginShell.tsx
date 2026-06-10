import React from 'react';
import type { PluginSessionState } from '../types';
import { useActivePluginContextStore } from '../activePluginContextStore';
import './PluginShell.scss';

interface PluginShellProps {
  session: PluginSessionState;
  title?: string;
  children: React.ReactNode;
}

export const PluginShell: React.FC<PluginShellProps> = ({
  session,
  title,
  children,
}) => {
  const requestAdvance = useActivePluginContextStore((s) => s.requestAdvance);

  return (
    <div className='plugin-shell' role='region' aria-label={title ?? 'Plugin'}>
      <div className='plugin-shell__header'>
        <span>{title ?? 'Plugin'}</span>
        {session.currentStep && (
          <span className='plugin-shell__badge'>{session.currentStep}</span>
        )}
      </div>

      <div className='plugin-shell__body'>{children}</div>

      {session.isWaiting && (
        <div className='plugin-shell__waiting'>
          <span>等待您的指令</span>
          <button
            className='plugin-shell__continue-btn'
            onClick={requestAdvance}
            type='button'
          >
            继续
          </button>
        </div>
      )}

      {session.stepError && (
        <div className='plugin-shell__error' role='alert'>
          {session.stepError}
        </div>
      )}
    </div>
  );
};
