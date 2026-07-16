interface Props {
  message: string;
  stage?: string;
}

export function ErrorAlert({ message, stage }: Props) {
  return (
    <div className="p-3 bg-red-900/20 border border-red-800/50 rounded text-sm">
      {stage && (
        <div className="text-xs text-red-300 mb-1 uppercase tracking-wide font-semibold">
          {stage} error
        </div>
      )}
      <pre className="text-red-400 font-mono whitespace-pre-wrap">{message}</pre>
    </div>
  );
}
