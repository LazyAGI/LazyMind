import React from 'react';
import type { PluginSessionState } from './types';
import { getPluginComponent } from './registry';
import { PluginShell } from './components/PluginShell';

interface PluginRendererProps {
  session: PluginSessionState;
}

/** Fallback view for plugins that have no registered component. */
const GenericPluginView: React.FC<{ session: PluginSessionState }> = ({ session }) => (
  <div className='plugin-generic'>
    <p>
      <strong>Step:</strong> {session.currentStep || '—'}
    </p>
    {session.stepProgress && (
      <p>{session.stepProgress.message}</p>
    )}
    {session.stepError && (
      <p className='plugin-generic__error' role='alert'>{session.stepError}</p>
    )}
    {session.isWaiting && (
      <p>Waiting for user confirmation…</p>
    )}
  </div>
);

export const PluginRenderer: React.FC<PluginRendererProps> = ({ session }) => {
  const PluginView = getPluginComponent(session.pluginId) ?? GenericPluginView;

  return (
    <PluginShell session={session} title={session.pluginId}>
      <PluginView session={session} />
    </PluginShell>
  );
};
