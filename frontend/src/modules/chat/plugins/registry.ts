import type React from 'react';
import type { PluginSessionState } from './types';
import { ImageCard } from './components/ImageCard';

type PluginViewComponent = React.ComponentType<{ session: PluginSessionState }>;

export const PLUGIN_REGISTRY: Record<string, PluginViewComponent> = {
  'image-plugin': ImageCard,
};
