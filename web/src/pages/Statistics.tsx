import { useState, useEffect } from 'react';
import { BarChart, Bar, XAxis, YAxis, Tooltip, ResponsiveContainer } from 'recharts';
import { api } from '../api/client';
import type { StatsResponse, TableStatsDTO } from '../types';

export function Statistics() {
  const [stats, setStats] = useState<StatsResponse | null>(null);
  const [loading, setLoading] = useState(false);

  const load = async () => {
    setLoading(true);
    try {
      const resp = await api.stats();
      setStats(resp);
    } catch {
      // ignore
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => { load(); }, []);

  if (loading) {
    return <div className="flex items-center justify-center h-full text-[#8892a4]">Loading statistics...</div>;
  }

  if (!stats) return null;

  return (
    <div className="overflow-auto p-6 flex flex-col gap-6">
      <div className="flex items-center justify-between">
        <h1 className="text-xl font-semibold">Table Statistics</h1>
        <button
          onClick={load}
          className="text-sm text-indigo-400 hover:text-indigo-300 transition-colors border border-indigo-500/40 px-3 py-1.5 rounded"
        >
          Refresh
        </button>
      </div>

      {Object.entries(stats.tables).map(([name, ts]) => (
        <TableCard key={name} name={name} ts={ts} />
      ))}
    </div>
  );
}

function TableCard({ name, ts }: { name: string; ts: TableStatsDTO }) {
  return (
    <div className="bg-[#1a1d27] border border-[#2e3347] rounded-lg overflow-hidden">
      <div className="flex items-center gap-3 px-4 py-3 border-b border-[#2e3347]">
        <h2 className="font-semibold">{name}</h2>
        <span className="text-sm text-[#8892a4]">
          {ts.rowCount.toLocaleString()} rows · {ts.pageCount} pages
        </span>
      </div>

      <div className="p-4 grid gap-4">
        {Object.entries(ts.columns).map(([col, cs]) => (
          <div key={col} className="bg-[#0f1117] rounded-lg p-3">
            <div className="flex items-center justify-between mb-2">
              <span className="font-mono text-sm text-indigo-300">{col}</span>
              <div className="flex gap-4 text-xs text-[#8892a4]">
                <span>NDV: <span className="text-white">{cs.distinctCount}</span></span>
                <span>Nulls: <span className="text-white">{cs.nullCount}</span></span>
                {cs.minValue != null && <span>Min: <span className="text-white">{cs.minValue}</span></span>}
                {cs.maxValue != null && <span>Max: <span className="text-white">{cs.maxValue}</span></span>}
              </div>
            </div>

            {cs.histogram && cs.histogram.length > 0 && (
              <div className="h-24">
                <ResponsiveContainer width="100%" height="100%">
                  <BarChart data={cs.histogram.map((b, i) => ({ name: `B${i + 1}`, freq: b.frequency }))}>
                    <XAxis dataKey="name" tick={{ fill: '#8892a4', fontSize: 10 }} />
                    <YAxis tick={{ fill: '#8892a4', fontSize: 10 }} />
                    <Tooltip
                      contentStyle={{ background: '#1e2130', border: '1px solid #2e3347', borderRadius: '6px' }}
                      labelStyle={{ color: '#e2e8f0' }}
                      itemStyle={{ color: '#818cf8' }}
                    />
                    <Bar dataKey="freq" fill="#6366f1" radius={[2, 2, 0, 0]} />
                  </BarChart>
                </ResponsiveContainer>
              </div>
            )}
          </div>
        ))}
      </div>
    </div>
  );
}
