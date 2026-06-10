import React from 'react';
import type { PluginSessionState } from '../types';
import './ImageCard.scss';

interface ImageCardProps {
  session: PluginSessionState;
}

export const ImageCard: React.FC<ImageCardProps> = ({ session }) => {
  const imageUrl = session.artifacts['image_url'] as string | undefined;
  const optimizedPrompt = session.artifacts['optimized_prompt'] as string | undefined;
  const isLoading = !imageUrl && !session.stepError;

  return (
    <div className='image-card'>
      {session.stepProgress && (
        <div>
          <div className='image-card__progress'>
            <div
              className='image-card__progress-bar'
              style={{ width: `${(session.stepProgress.progress ?? 0) * 100}%` }}
              role='progressbar'
              aria-valuenow={(session.stepProgress.progress ?? 0) * 100}
              aria-valuemin={0}
              aria-valuemax={100}
            />
          </div>
          <span className='image-card__progress-label'>
            {session.stepProgress.message}
          </span>
        </div>
      )}

      {isLoading && !session.stepProgress && (
        <div className='image-card__skeleton' aria-label='Loading image' role='status' />
      )}

      {imageUrl && (
        <div className='image-card__image-wrapper'>
          <img
            src={imageUrl}
            alt={optimizedPrompt ?? 'Generated image'}
            className='image-card__image'
          />
        </div>
      )}

      {optimizedPrompt && (
        <p className='image-card__prompt'>
          <span className='image-card__prompt-label'>Prompt: </span>
          {optimizedPrompt}
        </p>
      )}

      {session.stepError && (
        <div className='image-card__error' role='alert'>
          {session.stepError}
        </div>
      )}
    </div>
  );
};
