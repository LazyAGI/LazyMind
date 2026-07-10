import { FileTextOutlined, PictureOutlined, FileOutlined, CodeOutlined } from '@ant-design/icons';
import type { ReactNode } from 'react';

export const SLOT_TYPE_ICONS: Record<string, ReactNode> = {
  text: <FileTextOutlined />,
  image: <PictureOutlined />,
  file: <FileOutlined />,
  json: <CodeOutlined />,
};
