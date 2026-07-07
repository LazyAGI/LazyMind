import { useEffect, useRef, useState } from 'react';
import { Button, Input, Tooltip, message } from 'antd';
import { PlusOutlined, DeleteOutlined, FileOutlined } from '@ant-design/icons';
import type * as monacoType from 'monaco-editor';
import './index.scss';

// Shared monaco loader (same pattern as YamlEditor)
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

interface Props {
  /** scripts_content: JSON string of { "path": "content" } */
  value: string;
  onChange: (value: string) => void;
}

function parseScripts(raw: string): Record<string, string> {
  try {
    const parsed = JSON.parse(raw || '{}');
    if (typeof parsed === 'object' && parsed !== null && !Array.isArray(parsed)) {
      return parsed as Record<string, string>;
    }
  } catch {
    // ignore
  }
  return {};
}

function MonacoEditor({ value, onChange, language }: { value: string; onChange: (v: string) => void; language: string }) {
  const containerRef = useRef<HTMLDivElement>(null);
  const editorRef = useRef<monacoType.editor.IStandaloneCodeEditor | null>(null);
  const onChangeRef = useRef(onChange);
  onChangeRef.current = onChange;
  const externalRef = useRef(false);

  useEffect(() => {
    let disposed = false;
    let editor: monacoType.editor.IStandaloneCodeEditor | null = null;
    void loadMonaco().then((monaco) => {
      if (disposed || !containerRef.current) return;
      editor = monaco.editor.create(containerRef.current, {
        value,
        language,
        theme: 'vs',
        fontSize: 13,
        minimap: { enabled: false },
        scrollBeyondLastLine: false,
        automaticLayout: true,
        lineNumbers: 'on',
        tabSize: 4,
        insertSpaces: true,
      });
      editorRef.current = editor;
      editor.onDidChangeModelContent(() => {
        if (externalRef.current) return;
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

  useEffect(() => {
    const editor = editorRef.current;
    if (!editor) return;
    if (editor.getValue() === value) return;
    externalRef.current = true;
    editor.setValue(value);
    externalRef.current = false;
  }, [value]);

  return <div ref={containerRef} style={{ width: '100%', height: '100%' }} />;
}

export default function ScriptFilesEditor({ value, onChange }: Props) {
  const [scripts, setScripts] = useState<Record<string, string>>(() => parseScripts(value));
  const [selectedPath, setSelectedPath] = useState<string | null>(() => {
    const paths = Object.keys(parseScripts(value));
    return paths.length > 0 ? paths[0] : null;
  });
  const [newFileName, setNewFileName] = useState('');
  const [addingFile, setAddingFile] = useState(false);

  const emit = (next: Record<string, string>) => {
    setScripts(next);
    onChange(JSON.stringify(next));
  };

  const handleFileContentChange = (content: string) => {
    if (!selectedPath) return;
    emit({ ...scripts, [selectedPath]: content });
  };

  const handleAddFile = () => {
    const name = newFileName.trim();
    if (!name) return;
    const path = name.startsWith('scripts/') ? name : `scripts/${name}`;
    if (scripts[path] !== undefined) {
      message.warning('文件已存在');
      return;
    }
    const next = { ...scripts, [path]: '' };
    emit(next);
    setSelectedPath(path);
    setNewFileName('');
    setAddingFile(false);
  };

  const handleDeleteFile = (path: string) => {
    const next = { ...scripts };
    delete next[path];
    emit(next);
    if (selectedPath === path) {
      const remaining = Object.keys(next);
      setSelectedPath(remaining.length > 0 ? remaining[0] : null);
    }
  };

  const paths = Object.keys(scripts);

  return (
    <div className="script-files-editor">
      {/* Left: file tree */}
      <div className="sfe-sidebar">
        <div className="sfe-sidebar-header">
          <span className="sfe-sidebar-title">脚本文件</span>
          <Tooltip title="新建文件">
            <Button
              type="text"
              size="small"
              icon={<PlusOutlined />}
              onClick={() => setAddingFile(true)}
            />
          </Tooltip>
        </div>
        {addingFile && (
          <div className="sfe-new-file-row">
            <Input
              size="small"
              autoFocus
              value={newFileName}
              onChange={(e) => setNewFileName(e.target.value)}
              placeholder="tools.py"
              onPressEnter={handleAddFile}
              onBlur={() => { if (!newFileName.trim()) setAddingFile(false); }}
            />
            <Button size="small" type="primary" onClick={handleAddFile}>添加</Button>
          </div>
        )}
        {paths.length === 0 && !addingFile && (
          <p className="sfe-empty-hint">暂无脚本文件</p>
        )}
        {paths.map((path) => (
          <div
            key={path}
            className={`sfe-file-item${path === selectedPath ? ' sfe-file-item--active' : ''}`}
            onClick={() => setSelectedPath(path)}
          >
            <FileOutlined className="sfe-file-icon" />
            <span className="sfe-file-name" title={path}>{path.replace('scripts/', '')}</span>
            <Button
              className="sfe-file-delete"
              type="text"
              danger
              size="small"
              icon={<DeleteOutlined />}
              onClick={(e) => { e.stopPropagation(); handleDeleteFile(path); }}
            />
          </div>
        ))}
      </div>

      {/* Right: editor */}
      <div className="sfe-editor">
        {selectedPath ? (
          <MonacoEditor
            key={selectedPath}
            value={scripts[selectedPath] ?? ''}
            onChange={handleFileContentChange}
            language="python"
          />
        ) : (
          <div className="sfe-editor-placeholder">
            <p>选择左侧文件开始编辑，或点击 + 新建脚本文件</p>
          </div>
        )}
      </div>
    </div>
  );
}
