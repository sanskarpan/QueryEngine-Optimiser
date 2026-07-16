import { useState } from 'react';
import { Panel, Group, Separator } from 'react-resizable-panels';
import { SQLEditor } from '../components/Editor/SQLEditor';
import { Toolbar } from '../components/Editor/Toolbar';
import { ResultTable } from '../components/Results/ResultTable';
import { PlanTree } from '../components/PlanViewer/PlanTree';
import { OptimizationDiff } from '../components/PlanViewer/OptimizationDiff';
import { useQueryStore } from '../store/queryStore';
import { api } from '../api/client';
import type { ErrorDetails } from '../types';

type PlanTab = 'logical' | 'optimized' | 'physical' | 'steps';

export function Playground() {
  const {
    sql, setResult, setLoading, setError, setErrorDetails, addToHistory, plans,
    cancelQuery, setAbortController, abortController,
  } = useQueryStore();
  const [planTab, setPlanTab] = useState<PlanTab>('optimized');

  const runQuery = async () => {
    // Cancel any in-flight request before starting a new one.
    abortController?.abort();

    const controller = new AbortController();
    setAbortController(controller);
    setLoading(true);
    setError(null);
    setErrorDetails(null);
    try {
      const resp = await api.query(
        { sql, options: { explain: true, includeStats: true } },
        controller.signal,
      );
      if (resp.error) {
        setError(resp.error);
        setResult(null);
      } else {
        setResult(resp);
        addToHistory(sql);
      }
    } catch (e: unknown) {
      // Ignore abort errors (user cancelled)
      if (e instanceof Error && e.name === 'AbortError') return;

      // Try to parse error body from ky HTTPError (has .response property)
      if (e && typeof e === 'object' && 'response' in e) {
        try {
          const body = await (e as { response: Response }).response.json() as {
            error: string; stage?: string; line?: number; col?: number;
          };
          const details: ErrorDetails = {
            message: body.error ?? 'Unknown error',
            line: body.line ?? 0,
            col: body.col ?? 0,
            stage: body.stage ?? '',
          };
          setError(details.message);
          setErrorDetails(details);
        } catch {
          setError(e instanceof Error ? e.message : 'Request failed');
        }
      } else {
        setError(e instanceof Error ? e.message : 'Request failed');
      }
      setResult(null);
    } finally {
      setAbortController(null);
      setLoading(false);
    }
  };

  const planNode =
    plans &&
    (planTab === 'logical'
      ? plans.logical
      : planTab === 'optimized'
      ? plans.optimized
      : planTab === 'physical'
      ? plans.physical
      : null);

  return (
    <div className="flex flex-col h-full min-h-0">
      <Group orientation="horizontal" className="flex-1 min-h-0">
        {/* Left: Editor */}
        <Panel defaultSize={35} minSize={20}>
          <div className="flex flex-col h-full border-r border-[#2e3347]">
            <Toolbar onRun={runQuery} onCancel={cancelQuery} />
            <div className="flex-1 min-h-0">
              <SQLEditor onRun={runQuery} />
            </div>
          </div>
        </Panel>

        <Separator className="w-1 bg-[#2e3347] hover:bg-indigo-600 transition-colors cursor-col-resize" />

        {/* Right: Results + Plan */}
        <Panel defaultSize={65} minSize={30}>
          <Group orientation="vertical">
            {/* Results */}
            <Panel defaultSize={45} minSize={20}>
              <div className="flex flex-col h-full bg-[#0f1117]">
                <div className="px-3 py-2 border-b border-[#2e3347] text-xs font-semibold text-[#8892a4] uppercase tracking-wide bg-[#1a1d27] shrink-0">
                  Results
                </div>
                <div className="flex-1 min-h-0">
                  <ResultTable />
                </div>
              </div>
            </Panel>

            <Separator className="h-1 bg-[#2e3347] hover:bg-indigo-600 transition-colors cursor-row-resize" />

            {/* Plan Viewer */}
            <Panel defaultSize={55} minSize={20}>
              <div className="flex flex-col h-full bg-[#1a1d27]">
                {/* Tabs */}
                <div className="flex items-center border-b border-[#2e3347] bg-[#1a1d27] shrink-0">
                  {(['logical', 'optimized', 'physical', 'steps'] as PlanTab[]).map((tab) => (
                    <button
                      key={tab}
                      onClick={() => setPlanTab(tab)}
                      className={`px-4 py-2 text-xs font-medium uppercase tracking-wide transition-colors border-b-2 ${
                        planTab === tab
                          ? 'border-indigo-500 text-indigo-400'
                          : 'border-transparent text-[#8892a4] hover:text-white'
                      }`}
                    >
                      {tab === 'steps' ? 'Opt Steps' : tab}
                    </button>
                  ))}
                </div>
                <div className="flex-1 min-h-0">
                  {planTab === 'steps' ? (
                    <OptimizationDiff />
                  ) : (
                    <PlanTree plan={planNode ?? null} showOptHints={planTab === 'optimized'} />
                  )}
                </div>
              </div>
            </Panel>
          </Group>
        </Panel>
      </Group>
    </div>
  );
}
