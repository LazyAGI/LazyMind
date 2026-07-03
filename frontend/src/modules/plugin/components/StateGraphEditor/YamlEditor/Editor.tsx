import { useEffect, useRef } from 'react';
import type * as monacoType from 'monaco-editor';
import type { ValidationError } from '../core/validator';

interface Props {
  value: string;
  onChange: (value: string) => void;
  errors: ValidationError[];
}

// Lazily loaded monaco instance
let monacoInstance: typeof monacoType | null = null;
let loadingPromise: Promise<typeof monacoType> | null = null;

async function loadMonaco(): Promise<typeof monacoType> {
  if (monacoInstance) return monacoInstance;
  if (loadingPromise) return loadingPromise;
  loadingPromise = import('monaco-editor').then((mod) => {
    monacoInstance = mod;
    return mod;
  });
  return loadingPromise;
}

export default function Editor({ value, onChange, errors }: Props) {
  const containerRef = useRef<HTMLDivElement>(null);
  const editorRef = useRef<monacoType.editor.IStandaloneCodeEditor | null>(null);
  const onChangeRef = useRef(onChange);
  onChangeRef.current = onChange;

  // Track external value updates (from graph canvas)
  const externalUpdateRef = useRef(false);

  useEffect(() => {
    let disposed = false;
    let editor: monacoType.editor.IStandaloneCodeEditor | null = null;

    void loadMonaco().then((monaco) => {
      if (disposed || !containerRef.current) return;

      editor = monaco.editor.create(containerRef.current, {
        value,
        language: 'yaml',
        theme: 'vs',
        fontSize: 13,
        minimap: { enabled: false },
        scrollBeyondLastLine: false,
        automaticLayout: true,
        lineNumbers: 'on',
        tabSize: 2,
        insertSpaces: true,
        wordWrap: 'on',
      });

      editorRef.current = editor;

      editor.onDidChangeModelContent(() => {
        if (externalUpdateRef.current) return;
        onChangeRef.current(editor!.getValue());
      });
    });

    return () => {
      disposed = true;
      editor?.dispose();
      editorRef.current = null;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // Sync external value changes (e.g. from graph operations) without triggering onChange
  useEffect(() => {
    const editor = editorRef.current;
    if (!editor) return;
    if (editor.getValue() === value) return;
    externalUpdateRef.current = true;
    editor.setValue(value);
    externalUpdateRef.current = false;
  }, [value]);

  // Set error markers in the editor
  useEffect(() => {
    void loadMonaco().then((monaco) => {
      const editor = editorRef.current;
      if (!editor) return;
      const model = editor.getModel();
      if (!model) return;

      const markers: monacoType.editor.IMarkerData[] = errors.map((err) => ({
        severity: monaco.MarkerSeverity.Error,
        message: err.message,
        startLineNumber: err.line ?? 1,
        startColumn: 1,
        endLineNumber: err.line ?? 1,
        endColumn: Number.MAX_SAFE_INTEGER,
      }));

      monaco.editor.setModelMarkers(model, 'stategraph', markers);
    });
  }, [errors]);

  return (
    <div
      ref={containerRef}
      style={{ width: '100%', height: '100%', minHeight: 300 }}
      aria-label="YAML 编辑器"
    />
  );
}
