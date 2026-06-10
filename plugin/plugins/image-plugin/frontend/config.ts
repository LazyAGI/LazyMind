export const IMAGE_PLUGIN_CONFIG = {
  id: 'image-plugin',
  name: 'AI 图片生成',
  steps: [
    { id: 'optimize_prompt', label: '优化提示词' },
    { id: 'generate_image', label: '生成图片' },
  ],
} as const;
