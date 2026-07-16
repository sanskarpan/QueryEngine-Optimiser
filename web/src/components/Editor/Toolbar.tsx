import { useQueryStore } from '../../store/queryStore';

const SAMPLE_QUERIES = [
  { label: 'Filter by country', sql: "SELECT * FROM customers WHERE country = 'US' LIMIT 10" },
  { label: 'Join: orders + customers', sql: "SELECT c.name, COUNT(o.id) as orders FROM customers c JOIN orders o ON c.id = o.customer_id GROUP BY c.name ORDER BY orders DESC LIMIT 10" },
  { label: 'Three-way join', sql: "SELECT c.name, p.name, SUM(o.amount) as total FROM orders o JOIN customers c ON o.customer_id = c.id JOIN products p ON o.product_id = p.id GROUP BY c.name, p.name ORDER BY total DESC LIMIT 5" },
  { label: 'Aggregation + HAVING', sql: "SELECT customer_id, SUM(amount) total FROM orders GROUP BY customer_id HAVING COUNT(*) > 5 ORDER BY total DESC LIMIT 10" },
  { label: 'Status breakdown', sql: "SELECT status, COUNT(*) as cnt FROM orders GROUP BY status ORDER BY cnt DESC" },
  { label: 'EXISTS subquery', sql: "SELECT id, name FROM customers WHERE EXISTS (SELECT 1 FROM orders WHERE orders.customer_id = customers.id) LIMIT 10" },
];

interface Props {
  onRun: () => void;
  onCancel: () => void;
}

export function Toolbar({ onRun, onCancel }: Props) {
  const { isLoading, setSql, history, clearHistory } = useQueryStore();

  return (
    <div className="flex items-center gap-2 px-3 py-2 border-b border-[#2e3347] bg-[#1a1d27] shrink-0 flex-wrap">
      {isLoading ? (
        <button
          onClick={onCancel}
          className="px-4 py-1.5 bg-red-700 hover:bg-red-600 text-white text-sm font-medium rounded transition-colors flex items-center gap-1.5"
        >
          <span className="w-2 h-2 bg-white rounded-sm inline-block" />
          Cancel
        </button>
      ) : (
        <button
          onClick={onRun}
          className="px-4 py-1.5 bg-indigo-600 hover:bg-indigo-500 text-white text-sm font-medium rounded transition-colors"
          title="Run query (Ctrl+Enter)"
        >
          Run
          <kbd className="ml-2 text-[10px] opacity-60 font-mono">Ctrl+↵</kbd>
        </button>
      )}

      <select
        className="text-sm bg-[#1e2130] border border-[#2e3347] text-[#8892a4] rounded px-2 py-1.5 cursor-pointer hover:border-indigo-500 transition-colors"
        defaultValue=""
        onChange={(e) => {
          if (e.target.value) setSql(e.target.value);
          e.target.value = '';
        }}
      >
        <option value="" disabled>Sample queries...</option>
        {SAMPLE_QUERIES.map((q) => (
          <option key={q.label} value={q.sql}>{q.label}</option>
        ))}
      </select>

      {history.length > 0 && (
        <>
          <select
            className="text-sm bg-[#1e2130] border border-[#2e3347] text-[#8892a4] rounded px-2 py-1.5 cursor-pointer hover:border-indigo-500 transition-colors max-w-[180px]"
            defaultValue=""
            onChange={(e) => {
              if (e.target.value) setSql(e.target.value);
              e.target.value = '';
            }}
          >
            <option value="" disabled>History ({history.length})...</option>
            {history.map((q, i) => (
              <option key={i} value={q} title={q}>
                {q.length > 60 ? q.slice(0, 60) + '…' : q}
              </option>
            ))}
          </select>
          <button
            onClick={clearHistory}
            title="Clear query history"
            className="text-xs text-[#8892a4] hover:text-red-400 transition-colors px-1.5 py-1 rounded hover:bg-red-900/20"
          >
            Clear
          </button>
        </>
      )}
    </div>
  );
}
