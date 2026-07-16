import { useState, useEffect } from 'react';
import { api } from '../api/client';
import type { TableInfo } from '../types';

export function Schema() {
  const [tables, setTables] = useState<TableInfo[]>([]);
  const [selected, setSelected] = useState<TableInfo | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [seeding, setSeeding] = useState(false);

  const load = async () => {
    try {
      const resp = await api.schema();
      setTables(resp.tables);
      if (resp.tables.length > 0 && !selected) {
        setSelected(resp.tables[0]);
      }
    } catch {
      setError('Failed to load schema');
    }
  };

  useEffect(() => { load(); }, []);

  const seed = async () => {
    setSeeding(true);
    try {
      await api.seed();
      await load();
    } catch {
      setError('Seed failed');
    } finally {
      setSeeding(false);
    }
  };

  return (
    <div className="flex h-full min-h-0">
      {/* Sidebar */}
      <div className="w-56 flex-shrink-0 border-r border-[#2e3347] bg-[#1a1d27] flex flex-col">
        <div className="flex items-center justify-between px-3 py-2 border-b border-[#2e3347]">
          <span className="text-xs font-semibold text-[#8892a4] uppercase tracking-wide">Tables</span>
          <button
            onClick={seed}
            disabled={seeding}
            className="text-xs text-indigo-400 hover:text-indigo-300 transition-colors disabled:opacity-50"
          >
            {seeding ? 'Seeding...' : 'Re-seed'}
          </button>
        </div>
        {error && <div className="px-3 py-2 text-red-400 text-xs">{error}</div>}
        <div className="flex-1 overflow-auto">
          {tables.map((t) => (
            <button
              key={t.name}
              onClick={() => setSelected(t)}
              className={`w-full text-left px-3 py-2 text-sm transition-colors flex items-center justify-between ${
                selected?.name === t.name
                  ? 'bg-indigo-600/20 text-indigo-300 border-l-2 border-indigo-500'
                  : 'text-[#e2e8f0] hover:bg-[#1e2130] border-l-2 border-transparent'
              }`}
            >
              <span>{t.name}</span>
              <span className="text-xs text-[#8892a4]">{t.rowCount}</span>
            </button>
          ))}
        </div>
      </div>

      {/* Column details */}
      <div className="flex-1 overflow-auto p-6">
        {selected ? (
          <div>
            <div className="flex items-center gap-3 mb-4">
              <h2 className="text-lg font-semibold">{selected.name}</h2>
              <span className="text-sm text-[#8892a4] bg-[#1e2130] px-2 py-0.5 rounded">
                {selected.rowCount.toLocaleString()} rows
              </span>
            </div>
            <table className="w-full text-sm border-collapse">
              <thead>
                <tr className="border-b border-[#2e3347]">
                  {['Column', 'Type', 'Nullable', 'PK'].map((h) => (
                    <th key={h} className="text-left px-3 py-2 text-[#8892a4] font-medium">{h}</th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {selected.columns.map((col) => (
                  <tr key={col.name} className="border-b border-[#1e2130] hover:bg-[#1e2130]/50">
                    <td className="px-3 py-2 font-mono">{col.name}</td>
                    <td className="px-3 py-2 text-indigo-400">{col.type}</td>
                    <td className="px-3 py-2 text-[#8892a4]">{col.nullable ? 'YES' : 'NO'}</td>
                    <td className="px-3 py-2">
                      {col.primaryKey && <span className="text-xs bg-amber-900/40 text-amber-400 px-1.5 py-0.5 rounded">PK</span>}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        ) : (
          <div className="flex items-center justify-center h-full text-[#8892a4] text-sm">
            Select a table
          </div>
        )}
      </div>
    </div>
  );
}
