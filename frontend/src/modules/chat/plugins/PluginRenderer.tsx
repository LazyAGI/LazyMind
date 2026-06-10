import React from 'react';
import type { PluginSessionState } from './types';
import { PLUGIN_REGISTRY } from './registry';
import { PluginShell } from './components/PluginShell';

interface PluginRendererProps {
  session: PluginSessionState;
}

export const PluginRenderer: React.FC<PluginRendererProps> = ({ session }) => {
  const PluginView = PLUGIN_REGISTRY[session.pluginId];

  if (!PluginView) {
    return null;
  }

  return (
    <PluginShell session={session} title={session.pluginId}>
      <PluginView session={session} />
    </PluginShell>
  );
};
