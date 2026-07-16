interface Props {
  label?: string;
}

export function LoadingSpinner({ label = 'Loading...' }: Props) {
  return (
    <div className="flex items-center gap-2 text-[#8892a4] text-sm">
      <div className="w-4 h-4 border-2 border-indigo-500 border-t-transparent rounded-full animate-spin shrink-0" />
      <span>{label}</span>
    </div>
  );
}
