import { describe, expect, it } from 'vitest';

import { isVideoArtifactValue } from './artifactMedia';

describe('workflow artifact media', () => {
  it('recognizes video files from canonical artifact paths', () => {
    expect(isVideoArtifactValue({
      path: '/var/lib/lazymind/uploads/workflow-artifacts/session/attempt/result.mp4',
    })).toBe(true);
  });

  it('recognizes declared video MIME types', () => {
    expect(isVideoArtifactValue({ name: 'output', mime_type: 'video/webm' })).toBe(true);
  });

  it('does not classify ordinary files or GIF images as video', () => {
    expect(isVideoArtifactValue({ path: '/tmp/report.pdf' })).toBe(false);
    expect(isVideoArtifactValue({ path: '/tmp/animation.gif' })).toBe(false);
  });
});
