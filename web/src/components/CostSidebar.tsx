import type { ViewerEvent } from '../lib/types';
import { aggregateUsage, formatTokenCount, formatUSD } from '../lib/format';

export function CostSidebar({ events }: { events: ViewerEvent[] }) {
  const usage = aggregateUsage(events);

  return (
    <aside className="flex flex-col gap-2 overflow-hidden rounded-lg border border-surface-0 bg-base p-3">
      <div>
        <p className="text-[10px] font-medium uppercase tracking-[0.12em] text-overlay-0">
          Dashboard
        </p>
        <h2 className="text-[13px] font-semibold text-text">Session Cost</h2>
      </div>

      {usage.totalTokens === 0 && usage.totalCostUSD === 0 ? (
        <p className="text-[11px] text-overlay-1">No usage data yet.</p>
      ) : (
        <div className="flex flex-col gap-3">
          <div className="rounded-md border border-surface-0 bg-mantle p-2.5">
            <p className="text-[10px] font-medium uppercase tracking-wider text-overlay-0">
              Total Cost
            </p>
            <p className="mt-1 font-mono text-[18px] font-semibold text-green">
              {formatUSD(usage.totalCostUSD)}
            </p>
          </div>

          <div className="flex flex-col gap-1.5">
            <p className="text-[10px] font-medium uppercase tracking-wider text-yellow">
              Token Count
            </p>
            <StatRow label="Input" value={formatTokenCount(usage.inputTokens)} />
            <StatRow label="Output" value={formatTokenCount(usage.outputTokens)} />
            {usage.cacheCreationTokens > 0 && (
              <StatRow label="Cache write" value={formatTokenCount(usage.cacheCreationTokens)} />
            )}
            {usage.cacheReadTokens > 0 && (
              <StatRow label="Cache read" value={formatTokenCount(usage.cacheReadTokens)} />
            )}
            <StatRow
              label="Total"
              value={formatTokenCount(usage.totalTokens)}
              valueClass="text-yellow font-semibold"
            />
          </div>

          <div className="flex flex-col gap-1.5">
            <p className="text-[10px] font-medium uppercase tracking-wider text-green">
              Cost Breakdown
            </p>
            <StatRow label="Input" value={formatUSD(usage.inputCostUSD)} />
            <StatRow label="Output" value={formatUSD(usage.outputCostUSD)} />
            {usage.cacheCreationCostUSD > 0 && (
              <StatRow label="Cache write" value={formatUSD(usage.cacheCreationCostUSD)} />
            )}
            {usage.cacheReadCostUSD > 0 && (
              <StatRow label="Cache read" value={formatUSD(usage.cacheReadCostUSD)} />
            )}
          </div>
        </div>
      )}
    </aside>
  );
}

function StatRow({
  label,
  value,
  valueClass,
}: {
  label: string;
  value: string;
  valueClass?: string;
}) {
  return (
    <div className="flex items-center justify-between">
      <span className="text-[11px] text-overlay-0">{label}</span>
      <span className={`font-mono text-[11px] tabular-nums ${valueClass ?? 'text-text'}`}>
        {value}
      </span>
    </div>
  );
}
