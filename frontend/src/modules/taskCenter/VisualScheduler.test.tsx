import { useState } from 'react';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { VisualScheduler } from './ScheduleList';
vi.mock('react-i18next', async original => ({ ...await original<typeof import('react-i18next')>(), useTranslation: () => ({ t: (key: string) => key }) }));
vi.mock('@/modules/chat/utils/request', () => ({ KnowledgeBaseServiceApi: vi.fn() }));
vi.mock('@/modules/chat/utils/chunkUpload', () => ({ uploadFileInChunks: vi.fn() }));
vi.mock('@/components/request', () => ({ axiosInstance: {}, BASE_URL: '', localizeErrorCode: vi.fn() }));
afterEach(cleanup);
function Controlled({ initial }: { initial: string }) {
  const [value, setValue] = useState(initial);
  return <><VisualScheduler value={value} onChange={setValue} /><output data-testid="cron">{value}</output></>;
}
function choose(label: string) {
  fireEvent.mouseDown(screen.getByRole('combobox', { name: 'notifications.frequencyLabel' }));
  fireEvent.click(screen.getByText(label, { selector: '.ant-select-item-option-content' }));
}
it('switches common presets without changing the execution time', () => {
  render(<Controlled initial="48 15 * * 1,2,3,4,5" />);
  choose('notifications.frequencyDaily');
  expect(screen.getByTestId('cron')).toHaveTextContent('48 15 * * *');
  choose('notifications.frequencyWeekly');
  fireEvent.click(screen.getByRole('button', { name: 'taskCenter.weekdayShort0' }));
  expect(screen.getByTestId('cron')).toHaveTextContent('48 15 * * 1,2,3,4,5,6');
  choose('notifications.frequencyWorkdays');
  expect(screen.getByTestId('cron')).toHaveTextContent('48 15 * * 1,2,3,4,5');
});
it('preserves existing custom monthly schedules until deliberately changed', () => {
  render(<Controlled initial="@every:2:month;30 8 1,-1 * *" />);
  expect(screen.getByTestId('cron')).toHaveTextContent('@every:2:month;30 8 1,-1 * *');
  expect(screen.getByRole('spinbutton')).toHaveValue(2);
  fireEvent.change(screen.getByRole('spinbutton'), { target: { value: '3' } });
  expect(screen.getByTestId('cron')).toHaveTextContent('@every:3:month;30 8 1,-1 * *');
});
it('does not turn the last selected weekday into an accidental daily schedule', () => {
  render(<Controlled initial="0 9 * * 1" />);
  fireEvent.click(screen.getByRole('button', { name: 'taskCenter.weekdayShort1' }));
  expect(screen.getByTestId('cron')).toHaveTextContent('0 9 * * 1');
});
