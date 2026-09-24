import dayjs from 'dayjs';

type TFunc = (key: string, options?: Record<string, unknown>) => string;

export function parseCadence(value: string): { interval: number; unit?: 'week' | 'month'; cron: string } {
  const match = value.match(/^@every:(\d+):(week|month);(.+)$/);
  return match ? { interval: Math.max(1, Number(match[1])), unit: match[2] as 'week' | 'month', cron: match[3] } : { interval: 1, cron: value };
}

export function sortMonthDays(days: number[]): number[] {
  return [...days].sort((a, b) => {
    if (a > 0 && b < 0) return -1;
    if (a < 0 && b > 0) return 1;
    return a > 0 ? a - b : Math.abs(a) - Math.abs(b);
  });
}

export function formatMonthDays(days: number[], t: TFunc): string {
  const sorted = sortMonthDays(days);
  const regular = sorted.filter((day) => day > 0);
  const fromEnd = sorted.filter((day) => day < 0).map((day) => Math.abs(day));
  const separator = t('taskCenter.scheduleListSeparator');
  return [
    regular.length ? t('taskCenter.cronMonthDays', { days: regular.join(separator) }) : '',
    fromEnd.length ? t('taskCenter.cronMonthDaysFromEnd', { days: fromEnd.join(separator) }) : '',
  ].filter(Boolean).join(separator);
}

export function parseMonthDayField(field: string): number[] {
  const days: number[] = [];
  field.split(',').forEach((rawToken) => {
    const token = rawToken.trim();
    const range = token.match(/^(-?\d+)-(-?\d+)$/);
    if (range) {
      const start = Number(range[1]);
      const end = Number(range[2]);
      const step = start <= end ? 1 : -1;
      for (let day = start; day !== end + step; day += step) days.push(day);
      return;
    }
    const day = Number(token);
    if (Number.isInteger(day)) days.push(day);
  });
  return sortMonthDays([...new Set(days.filter((day) => (day >= 1 && day <= 31) || (day >= -4 && day <= -1)))]);
}

export function parseCronExpr(cron: string): { weekdays: number[]; time: dayjs.Dayjs } {
  const parts = parseCadence(cron).cron.trim().split(/\s+/);
  const minute = parseInt(parts[0] ?? '0', 10) || 0;
  const hour = parseInt(parts[1] ?? '0', 10) || 0;
  const dowStr = parts[4] ?? '*';
  const weekdays =
    dowStr === '*'
      ? []
      : dowStr.split(',').map((v) => parseInt(v, 10)).filter((v) => !isNaN(v));
  return { weekdays, time: dayjs().hour(hour).minute(minute).second(0) };
}

export function describeCron(cron: string, t: TFunc): string {
  const cadence = parseCadence(cron);
  const fields = cadence.cron.trim().split(/\s+/);
  if (fields.length === 5 && fields[2] !== '*') {
    const days = formatMonthDays(parseMonthDayField(fields[2]), t);
    const time = `${String(fields[1]).padStart(2, '0')}:${String(fields[0]).padStart(2, '0')}`;
    return cadence.interval > 1
      ? t('taskCenter.cronMonthlyInterval', { interval: cadence.interval, days, time })
      : t('taskCenter.cronMonthly', { days, time });
  }
  const { weekdays, time } = parseCronExpr(cron);
  const timeStr = time.format('HH:mm');
  if (weekdays.length === 0) return t('taskCenter.cronDaily', { time: timeStr });
  const sep = t('taskCenter.weekdaySeparator');
  const labels = weekdays.map((d) => t(`taskCenter.weekdayFull${d}`)).join(sep);
  const weekly = t('taskCenter.cronWeekdays', { days: labels, time: timeStr });
  return cadence.interval > 1 ? t('taskCenter.cronWeeklyInterval', { interval: cadence.interval, schedule: weekly }) : weekly;
}
