export function EmptyPanel({ title, body }: { title: string; body: string }) {
  return (
    <div className="flex flex-col items-center justify-center gap-1.5 rounded-lg border border-dashed border-surface-0 bg-mantle/50 px-6 py-10 text-center">
      <h3 className="text-[12px] font-medium text-subtext-0">{title}</h3>
      <p className="max-w-[280px] text-[11px] leading-relaxed text-overlay-1">
        {body}
      </p>
    </div>
  );
}
