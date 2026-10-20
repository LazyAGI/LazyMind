import { expect, it } from 'vitest';
import FileUtils from './file';

it('does not mistake a blob origin for a file extension', () => {
  expect(FileUtils.getFileTypeFromURI('blob:http://127.0.0.1:8090/1234')).toBe('');
  expect(FileUtils.getFileTypeFromURI('blob:https://app.example.com/1234')).toBe('');
  expect(FileUtils.getFileTypeFromURI('data:text/plain,a.txt')).toBe('');
  expect(FileUtils.getFileTypeFromURI('/files/会议纪要.MD?token=1')).toBe('md');
  expect(FileUtils.getFileTypeFromURI('验收报告.md')).toBe('md');
});
