import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { ConfigProvider } from 'antd';
import DatabaseConnectionModal from './DatabaseConnectionModal';

vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: (key: string) => key }) }));

describe('database connection names', () => {
  it.each([false, true])('rejects more than 255 characters and retains input (editing=%s)', async (editing) => {
    const onSubmit = vi.fn().mockResolvedValue(undefined);
    render(<ConfigProvider theme={{ token: { motion: false } }}><DatabaseConnectionModal open onSubmit={onSubmit} onCancel={vi.fn()} editing={editing ? {
      id: 'p2-test', display_name: 'Existing', description: '', db_type: 'postgresql',
      host: 'db.example.invalid', port: 5432, database_name: 'demo', username: 'test',
      options: {}, is_verified: false, create_time: '', update_time: '',
    } : undefined} /></ConfigProvider>);
    if (!editing) {
      for (const [label, value] of [
        ['Host', 'db.example.invalid'], ['DatabaseName', 'demo'],
        ['Username', 'test'], ['Password', 'test-only-password'],
      ]) fireEvent.change(screen.getByLabelText(`admin.dataSourceDatabase${label}`), { target: { value } });
    }
    const name = screen.getByLabelText('admin.dataSourceDatabaseName');
    fireEvent.change(name, { target: { value: '名'.repeat(256) } });
    fireEvent.click(screen.getByRole('button', { name: 'common.save' }));
    await waitFor(() => expect(screen.getByText('admin.dataSourceDatabaseNameTooLong')).toBeVisible());
    expect(onSubmit).not.toHaveBeenCalled();
    expect(name).toHaveValue('名'.repeat(256));
    fireEvent.change(name, { target: { value: '🧪'.repeat(255) } });
    fireEvent.click(screen.getByRole('button', { name: 'common.save' }));
    await waitFor(() => expect(onSubmit).toHaveBeenCalledWith(expect.objectContaining({ display_name: '🧪'.repeat(255) })));
  });
});
