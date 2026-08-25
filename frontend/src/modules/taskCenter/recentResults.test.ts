import { describe, expect, it } from 'vitest';
import { isTaskFinishedWithinDays } from './recentResults';

const NOW = Date.parse('2026-08-20T06:00:00.000Z');

describe('isTaskFinishedWithinDays', () => {
  it('includes the exact rolling seven-day boundary', () => {
    expect(isTaskFinishedWithinDays({
      finished_at: '2026-08-13T06:00:00.000Z',
      updated_at: '2026-08-13T06:00:00.000Z',
    }, 7, NOW)).toBe(true);
  });

  it('excludes results older than seven days and results dated in the future', () => {
    expect(isTaskFinishedWithinDays({
      finished_at: '2026-08-13T05:59:59.999Z',
      updated_at: '2026-08-20T05:00:00.000Z',
    }, 7, NOW)).toBe(false);
    expect(isTaskFinishedWithinDays({
      finished_at: '2026-08-20T06:00:00.001Z',
      updated_at: '2026-08-20T05:00:00.000Z',
    }, 7, NOW)).toBe(false);
  });

  it('falls back to updated_at when finished_at is unavailable', () => {
    expect(isTaskFinishedWithinDays({
      updated_at: '2026-08-19T06:00:00.000Z',
    }, 7, NOW)).toBe(true);
  });
});
