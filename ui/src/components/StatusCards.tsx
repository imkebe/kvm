import React from "react";

import { cx } from "@/cva.config";

interface Props {
  title: string;
  status: string;
  icon?: React.FC<{ className: string | undefined }>;
  iconClassName?: string;
  statusIndicatorClassName?: string;
  // A control at the right edge of the card, such as a button.
  action?: React.ReactNode;
  // Replaces the status dot.
  indicator?: React.ReactNode;
}

export default function StatusCard({
  title,
  status,
  icon: Icon,
  iconClassName,
  statusIndicatorClassName,
  action,
  indicator,
}: Props) {
  return (
    <div className="flex items-center gap-x-3 rounded-md border border-slate-800/20 bg-white px-2 py-1.5 dark:border-slate-600 dark:bg-slate-800 dark:text-white">
      {Icon ? (
        <span>
          <Icon className={cx(iconClassName, "shrink-0")} />
        </span>
      ) : null}

      <div className="min-w-0 flex-1 space-y-1">
        <div className="truncate text-xs leading-none font-semibold transition">{title}</div>
        <div className="text-xs leading-none">
          {/* One line, so every card in the ribbon has the same height. */}
          <div className="flex min-w-0 items-center gap-x-1">
            {indicator ?? (
              <div
                className={cx(
                  "h-2 w-2 shrink-0 rounded-full border transition",
                  statusIndicatorClassName,
                )}
              />
            )}
            <span className="truncate whitespace-nowrap transition" title={status}>
              {status}
            </span>
          </div>
        </div>
      </div>
      {action && <div className="ml-auto shrink-0">{action}</div>}
    </div>
  );
}
