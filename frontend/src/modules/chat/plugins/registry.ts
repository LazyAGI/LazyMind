import React from 'react';
import type { PluginSessionState } from './types';
import { ImageCard } from './components/ImageCard';

export type PluginViewComponent = React.ComponentType<{ session: PluginSessionState }>;

const pluginRegistry = new Map<string, PluginViewComponent>();

/**
 * Register a view component for a plugin ID.
 * Call this once per plugin, typically at module load time.
 */
export function registerPlugin(pluginId: string, component: PluginViewComponent): void {
  pluginRegistry.set(pluginId, component);
}

/**
 * Look up a registered component by plugin ID.
 * Returns undefined when no component is registered for the given ID.
 */
export function getPluginComponent(pluginId: string): PluginViewComponent | undefined {
  return pluginRegistry.get(pluginId);
}

// Built-in plugin registrations
registerPlugin('image-plugin', ImageCard);
