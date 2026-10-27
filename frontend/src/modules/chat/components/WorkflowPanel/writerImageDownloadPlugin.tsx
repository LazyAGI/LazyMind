import { $isImageNode, Cell, editImageToolbarComponent$, imagePreviewHandler$, realmPlugin, useCellValue, type NodeRef } from '@mdxeditor/editor';
import { useLexicalComposerContext } from '@lexical/react/LexicalComposerContext';
import { $getNodeByKey } from 'lexical';
import { DownloadOutlined, LoadingOutlined } from '@ant-design/icons';
import { message } from 'antd';
import { useEffect, useRef, useState, type ComponentProps } from 'react';
import { useTranslation } from 'react-i18next';
import { downloadStream } from '@/modules/chat/utils/download';

type ImageToolbar = typeof editImageToolbarComponent$ extends NodeRef<infer T> ? T : never;
const downloadToolbar$ = Cell<ImageToolbar | null>(null);
const downloadImage$ = Cell<((source: string) => Promise<void>) | undefined>(undefined);

function ImageDownloadButton({ source: initialSource, preview, nodeKey }: { source: string; preview: string; nodeKey: string }) {
  const { t } = useTranslation();
  const [editor] = useLexicalComposerContext();
  const resolveImage = useCellValue(imagePreviewHandler$);
  const downloadImage = useCellValue(downloadImage$);
  const pending = useRef<AbortController>();
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    setBusy(false);
    return () => { pending.current?.abort(); pending.current = undefined; };
  }, [initialSource, preview]);

  const download = async () => {
    if (pending.current) return;
    const controller = new AbortController(); pending.current = controller; setBusy(true);
    const timeout = window.setTimeout(() => controller.abort(), 60_000);
    try {
      const source = editor.getEditorState().read(() => {
        const node = $getNodeByKey(nodeKey);
        return $isImageNode(node) ? node.getSrc() : initialSource;
      });
      if (downloadImage) {
        await downloadImage(source);
        return;
      }
      const url = resolveImage ? await resolveImage(source) : preview;
      controller.signal.throwIfAborted();
      if (!/^(https?:|blob:|data:image\/|\/)/i.test(url)) throw new Error('Image unavailable');
      const response = await fetch(url, { signal: controller.signal, credentials: 'same-origin' });
      if (!response.ok) throw new Error('Image download failed');
      const blob = await response.blob();
      controller.signal.throwIfAborted();
      if (!blob.size) throw new Error('Empty image');
      let filename = source.startsWith('data:') ? '' : source.split(/[?#]/)[0].split('/').pop() || '';
      try { filename = decodeURIComponent(filename); } catch { /* Keep an already decoded filename. */ }
      filename = filename.replace(/[<>:"/\\|?*\u0000-\u001f]/g, '_');
      if (!/\.(png|jpe?g|gif|webp|avif|svg|bmp)$/i.test(filename)) {
        const extension = ({ 'image/jpeg': 'jpg', 'image/gif': 'gif', 'image/webp': 'webp', 'image/avif': 'avif', 'image/svg+xml': 'svg' } as Record<string, string>)[blob.type] || 'png';
        filename = `image.${extension}`;
      }
      downloadStream(blob, filename);
    } catch {
      if (pending.current === controller) void message.error(t('chat.preview.downloadFailed'));
    } finally {
      window.clearTimeout(timeout);
      if (pending.current === controller) { pending.current = undefined; setBusy(false); }
    }
  };

  return <button type='button' className='writer-markdown-editor__image-download' title={t('chat.writerImages.downloadOne')}
    aria-label={t('chat.writerImages.downloadOne')} aria-busy={busy} disabled={busy}
    onMouseDown={event => { event.preventDefault(); event.stopPropagation(); }}
    onClick={event => { event.preventDefault(); event.stopPropagation(); void download(); }}>
    {busy ? <LoadingOutlined spin aria-hidden /> : <DownloadOutlined aria-hidden />}
  </button>;
}

// Extend the editor's own toolbar so its delete/settings behavior stays intact.
export const writerImageDownloadPlugin = realmPlugin<{ onDownload?: (source: string) => Promise<void> }>({
  init(realm, params) {
    realm.pub(downloadImage$, params?.onDownload);
    const DefaultToolbar = realm.getValue(editImageToolbarComponent$);
    const Toolbar: ImageToolbar = (props: ComponentProps<ImageToolbar>) => <div className='writer-markdown-editor__image-toolbar'>
      <ImageDownloadButton source={props.initialImagePath ?? props.imageSource} preview={props.imageSource} nodeKey={props.nodeKey} />
      <DefaultToolbar {...props} />
    </div>;
    realm.pub(downloadToolbar$, Toolbar);
    realm.pub(editImageToolbarComponent$, Toolbar);
  },
  update(realm, params) {
    realm.pub(downloadImage$, params?.onDownload);
    // imagePlugin restores its toolbar on updates; reapply the same component.
    const Toolbar = realm.getValue(downloadToolbar$);
    if (Toolbar) realm.pub(editImageToolbarComponent$, Toolbar);
  },
});
