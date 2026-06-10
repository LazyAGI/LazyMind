import React from 'react';
import type { PluginSessionState } from '../../../src/modules/chat/plugins/types';

interface ImagePluginViewProps {
  session: PluginSessionState;
}

export const ImagePluginView: React.FC<ImagePluginViewProps> = ({ session }) => {
  const imageUrl = session.artifacts['image_url'] as string | undefined;
  const optimizedPrompt = session.artifacts['optimized_prompt'] as string | undefined;
  const isLoading = !session.artifacts['image_url'] && !session.stepError;

  return (
    <div className='image-plugin-card'>
      {session.stepProgress && (
        <div className='image-plugin-card__progress'>
          <div
            className='image-plugin-card__progress-bar'
            style={{ width: `${(session.stepProgress.progress || 0) * 100}%` }}
          />
          <span className='image-plugin-card__progress-label'>
            {session.stepProgress.message}
          </span>
        </div>
      )}

      {isLoading && !session.stepProgress && (
        <div className='image-plugin-card__skeleton' />
      )}

      {imageUrl && (
        <div className='image-plugin-card__image-wrapper'>
          <img
            src={imageUrl}
            alt={optimizedPrompt || 'Generated image'}
            className='image-plugin-card__image'
          />
        </div>
      )}

      {optimizedPrompt && (
        <div className='image-plugin-card__prompt'>
          <span className='image-plugin-card__prompt-label'>Prompt: </span>
          <span className='image-plugin-card__prompt-text'>{optimizedPrompt}</span>
        </div>
      )}

      {session.stepError && (
        <div className='image-plugin-card__error' role='alert'>
          {session.stepError}
        </div>
      )}
    </div>
  );
};
