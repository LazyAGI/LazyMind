import { FileTextOutlined, PictureOutlined, FileOutlined, CodeOutlined } from '@ant-design/icons';
import type { ReactNode } from 'react';

export const SLOT_TYPE_ICONS: Record<string, ReactNode> = {
  text: <FileTextOutlined />,
  image: <PictureOutlined />,
  file: <FileOutlined />,
  json: <CodeOutlined />,
};

export const SLOT_TYPE_LABELS: Record<string, string> = {
  text: '文本',
  image: '图片',
  file: '文件',
  json: 'JSON',
};
