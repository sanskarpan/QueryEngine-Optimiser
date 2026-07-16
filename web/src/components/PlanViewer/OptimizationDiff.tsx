import { useQueryStore } from '../../store/queryStore';

export function OptimizationDiff() {
  const { optimizationSteps } = useQueryStore();

  if (!optimizationSteps || optimizationSteps.length === 0) {
    return (
      <div className="p-4 text-[#8892a4] text-sm">No optimization steps recorded.</div>
    );
  }

  const applied = optimizationSteps.filter((s) => s.applied);
  const skipped = optimizationSteps.filter((s) => !s.applied);

  return (
    <div className="flex flex-col gap-2 p-3 overflow-auto">
      {applied.length > 0 && (
        <div>
          <div className="text-xs font-semibold text-green-400 mb-1 uppercase tracking-wide">Applied ({applied.length})</div>
          <div className="flex flex-col gap-1">
            {applied.map((s, i) => (
              <div key={i} className="flex items-start gap-2 bg-green-900/20 border border-green-800/40 rounded px-3 py-2">
                <span className="text-green-400 mt-0.5">✓</span>
                <div>
                  <div className="text-sm font-medium text-green-300">{s.rule}</div>
                  <div className="text-xs text-[#8892a4]">{s.description}</div>
                </div>
              </div>
            ))}
          </div>
        </div>
      )}
      {skipped.length > 0 && (
        <div>
          <div className="text-xs font-semibold text-[#8892a4] mb-1 uppercase tracking-wide">Skipped ({skipped.length})</div>
          <div className="flex flex-col gap-1">
            {skipped.slice(0, 5).map((s, i) => (
              <div key={i} className="flex items-start gap-2 rounded px-3 py-1.5">
                <span className="text-[#8892a4] mt-0.5">–</span>
                <div className="text-xs text-[#8892a4]">{s.rule}</div>
              </div>
            ))}
            {skipped.length > 5 && (
              <div className="text-xs text-[#8892a4] px-3">and {skipped.length - 5} more...</div>
            )}
          </div>
        </div>
      )}
    </div>
  );
}
