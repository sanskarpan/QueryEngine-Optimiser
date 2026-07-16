import { useEffect, useRef, useCallback } from 'react';
import Editor, { type Monaco } from '@monaco-editor/react';
import type { editor } from 'monaco-editor';
import { useQueryStore } from '../../store/queryStore';
import { api } from '../../api/client';

interface Props {
  onRun: () => void;
}

export function SQLEditor({ onRun }: Props) {
  const { sql, setSql, errorDetails } = useQueryStore();
  const editorRef = useRef<editor.IStandaloneCodeEditor | null>(null);
  const monacoRef = useRef<Monaco | null>(null);
  const onRunRef = useRef(onRun);
  onRunRef.current = onRun;

  // Sync error markers whenever errorDetails changes
  useEffect(() => {
    const ed = editorRef.current;
    const mon = monacoRef.current;
    if (!ed || !mon) return;
    const model = ed.getModel();
    if (!model) return;

    if (errorDetails && errorDetails.line > 0) {
      mon.editor.setModelMarkers(model, 'sql', [
        {
          severity: mon.MarkerSeverity.Error,
          message: errorDetails.message,
          startLineNumber: errorDetails.line,
          startColumn: Math.max(1, errorDetails.col),
          endLineNumber: errorDetails.line,
          endColumn: Math.max(2, errorDetails.col + 8),
        },
      ]);
    } else {
      mon.editor.setModelMarkers(model, 'sql', []);
    }
  }, [errorDetails]);

  const handleMount = useCallback(async (ed: editor.IStandaloneCodeEditor, mon: Monaco) => {
    editorRef.current = ed;
    monacoRef.current = mon;

    // Ctrl/Cmd+Enter → run
    ed.addCommand(mon.KeyMod.CtrlCmd | mon.KeyCode.Enter, () => onRunRef.current());

    // SQL completion: table + column names fetched from schema API
    try {
      const schema = await api.schema();
      mon.languages.registerCompletionItemProvider('sql', {
        // eslint-disable-next-line @typescript-eslint/no-explicit-any
        provideCompletionItems: (model: any, position: any) => {
          const word = model.getWordUntilPosition(position);
          const range = {
            startLineNumber: position.lineNumber,
            endLineNumber: position.lineNumber,
            startColumn: word.startColumn,
            endColumn: word.endColumn,
          };
          // eslint-disable-next-line @typescript-eslint/no-explicit-any
          const suggestions: any[] = [];
          for (const table of schema.tables) {
            suggestions.push({
              label: table.name,
              kind: mon.languages.CompletionItemKind.Class,
              insertText: table.name,
              detail: `Table · ${table.rowCount} rows`,
              range,
            });
            for (const col of table.columns) {
              suggestions.push({
                label: col.name,
                kind: mon.languages.CompletionItemKind.Field,
                insertText: col.name,
                detail: `${table.name}.${col.name} (${col.type})`,
                range,
              });
            }
          }
          return { suggestions };
        },
      });
    } catch {
      // Schema unavailable – completions skipped
    }
  }, []);

  return (
    <div className="flex flex-col h-full min-h-0">
      <Editor
        height="100%"
        defaultLanguage="sql"
        theme="vs-dark"
        value={sql}
        onChange={(v) => setSql(v ?? '')}
        options={{
          minimap: { enabled: false },
          fontSize: 14,
          lineNumbers: 'on',
          wordWrap: 'on',
          scrollBeyondLastLine: false,
          automaticLayout: true,
        }}
        onMount={handleMount}
      />
    </div>
  );
}
