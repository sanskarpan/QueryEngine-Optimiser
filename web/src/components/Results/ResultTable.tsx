import { useState } from 'react';
import { useQueryStore } from '../../store/queryStore';
import { LoadingSpinner } from '../shared/LoadingSpinner';
import { ErrorAlert } from '../shared/ErrorAlert';

const PAGE_SIZE = 50;

export function ResultTable() {
  const { result, isLoading, error } = useQueryStore();
  const [page, setPage] = useState(0);

  if (isLoading) {
    return (
      <div className="flex items-center justify-center h-full">
        <LoadingSpinner label="Executing query..." />
      </div>
    );
  }

  if (error) {
    return (
      <div className="p-4">
        <ErrorAlert message={error} />
      </div>
    );
  }

  if (!result) {
    return (
      <div className="flex items-center justify-center h-full text-[#8892a4] text-sm">
        Run a query to see results
      </div>
    );
  }

  const { columns, rows, rowCount, executionTimeMs } = result;
  const totalPages = Math.ceil(rows.length / PAGE_SIZE);
  const pageStart = page * PAGE_SIZE;
  const pageEnd = Math.min(pageStart + PAGE_SIZE, rows.length);
  const pageRows = rows.slice(pageStart, pageEnd);

  const exportCSV = () => {
    const escape = (v: unknown) => {
      if (v == null) return '';
      const s = String(v);
      return s.includes(',') || s.includes('"') || s.includes('\n')
        ? `"${s.replace(/"/g, '""')}"` : s;
    };
    const lines = [columns.map(escape).join(',')];
    for (const row of rows) lines.push(row.map(escape).join(','));
    const blob = new Blob([lines.join('\n')], { type: 'text/csv' });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = 'results.csv';
    a.click();
    URL.revokeObjectURL(url);
  };

  const exportJSON = () => {
    const data = rows.map((row) =>
      Object.fromEntries(columns.map((col, i) => [col, row[i]]))
    );
    const blob = new Blob([JSON.stringify(data, null, 2)], { type: 'application/json' });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = 'results.json';
    a.click();
    URL.revokeObjectURL(url);
  };

  return (
    <div className="flex flex-col h-full min-h-0">
      {/* Header */}
      <div className="flex items-center justify-between px-3 py-2 border-b border-[#2e3347] shrink-0 bg-[#1a1d27]">
        <div className="flex items-center gap-3 text-sm text-[#8892a4]">
          <span className="text-indigo-400 font-medium">{rowCount} row{rowCount !== 1 ? 's' : ''}</span>
          <span>{executionTimeMs}ms</span>
          {result.stats && (
            <>
              <span title="Total rows scanned from storage">Scanned: {result.stats.rowsScanned}</span>
              {result.stats.hashJoins > 0 && (
                <span title="Hash join operations performed">HashJoins: {result.stats.hashJoins}</span>
              )}
              {result.stats.sortOperations > 0 && (
                <span title="Sort operations performed">Sorts: {result.stats.sortOperations}</span>
              )}
            </>
          )}
        </div>
        <div className="flex items-center gap-1">
          <button
            onClick={exportCSV}
            className="text-xs text-[#8892a4] hover:text-white transition-colors px-2 py-1 border border-[#2e3347] rounded hover:border-indigo-500"
            title="Download results as CSV"
          >
            CSV
          </button>
          <button
            onClick={exportJSON}
            className="text-xs text-[#8892a4] hover:text-white transition-colors px-2 py-1 border border-[#2e3347] rounded hover:border-indigo-500"
            title="Download results as JSON"
          >
            JSON
          </button>
        </div>
      </div>

      {/* Table */}
      <div className="flex-1 overflow-auto min-h-0">
        {rows.length === 0 ? (
          <div className="flex items-center justify-center h-full text-[#8892a4] text-sm">
            No rows returned
          </div>
        ) : (
          <table className="w-full text-sm border-collapse">
            <thead className="sticky top-0 bg-[#1e2130] z-10">
              <tr>
                {columns.map((col) => (
                  <th
                    key={col}
                    className="text-left px-3 py-2 text-[#8892a4] font-medium border-b border-[#2e3347] whitespace-nowrap"
                  >
                    {col}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {pageRows.map((row, i) => (
                <tr
                  key={pageStart + i}
                  className="border-b border-[#1e2130] hover:bg-[#1e2130]/50 transition-colors"
                >
                  {row.map((cell, j) => (
                    <td key={j} className="px-3 py-1.5 font-mono text-xs whitespace-nowrap">
                      {cell == null ? (
                        <span className="text-[#8892a4] italic">NULL</span>
                      ) : (
                        String(cell)
                      )}
                    </td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>

      {/* Pagination */}
      {rows.length > 0 && (
        <div className="flex items-center justify-between gap-2 px-3 py-2 border-t border-[#2e3347] text-xs text-[#8892a4] shrink-0">
          <span>
            Showing {pageStart + 1}–{pageEnd} of {rows.length} row{rows.length !== 1 ? 's' : ''}
          </span>
          {totalPages > 1 && (
            <div className="flex items-center gap-1">
              <button
                disabled={page === 0}
                onClick={() => setPage((p) => p - 1)}
                className="px-2 py-0.5 border border-[#2e3347] rounded disabled:opacity-40 hover:border-indigo-500 transition-colors"
              >
                ‹ Prev
              </button>
              <span className="px-1">
                {page + 1} / {totalPages}
              </span>
              <button
                disabled={page === totalPages - 1}
                onClick={() => setPage((p) => p + 1)}
                className="px-2 py-0.5 border border-[#2e3347] rounded disabled:opacity-40 hover:border-indigo-500 transition-colors"
              >
                Next ›
              </button>
            </div>
          )}
        </div>
      )}
    </div>
  );
}
