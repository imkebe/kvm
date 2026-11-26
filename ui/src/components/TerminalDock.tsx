import { useEffect, useState } from "react";
import { LuBell, LuTerminal } from "react-icons/lu";

import { cx } from "@/cva.config";
import { useUiStore } from "@hooks/stores";
import { TerminalProgress, useGuestTerminalStatus } from "@hooks/useGuestTerminalStatus";
import { m } from "@localizations/messages.js";

// Output within this long counts as activity.
const ACTIVE_MS = 1500;

const RING_RADIUS = 15;
const RING_LENGTH = 2 * Math.PI * RING_RADIUS;

// The ring around the icon: a progress arc, a spinning arc while busy, or a
// faint circle when idle.
function Ring({ progress, busy }: { progress: TerminalProgress; busy: boolean }) {
  const determinate =
    progress.state === "normal" || progress.state === "error" || progress.state === "paused";
  const spinning = progress.state === "indeterminate" || (busy && progress.state === "none");
  const color =
    progress.state === "error"
      ? "stroke-red-400"
      : progress.state === "paused"
        ? "stroke-amber-400"
        : "stroke-blue-400";
  return (
    <svg
      viewBox="0 0 36 36"
      className={cx("absolute inset-0 h-full w-full -rotate-90", spinning && "animate-spin")}
      aria-hidden
    >
      <circle
        cx="18"
        cy="18"
        r={RING_RADIUS}
        fill="none"
        strokeWidth="2.5"
        className="stroke-white/15"
      />
      {(determinate || spinning) && (
        <circle
          cx="18"
          cy="18"
          r={RING_RADIUS}
          fill="none"
          strokeWidth="2.5"
          strokeLinecap="round"
          className={cx(color, "transition-[stroke-dashoffset] duration-300")}
          strokeDasharray={RING_LENGTH}
          strokeDashoffset={
            determinate ? RING_LENGTH * (1 - progress.value / 100) : RING_LENGTH * 0.72
          }
        />
      )}
    </svg>
  );
}

// The minimized guest terminal: a button at the lower right that opens the
// panel again and shows what the shell is doing, as a dock or taskbar does.
export default function TerminalDock() {
  const setTerminalType = useUiStore(state => state.setTerminalType);
  const { title, running, lastExit, progress, lastOutput, unread, bell } = useGuestTerminalStatus();
  const [now, setNow] = useState(() => performance.now());

  // Activity fades by time, not by an event, so the button looks again once
  // the output is old enough; no timer runs while the terminal is idle.
  useEffect(() => {
    if (lastOutput <= 0) return;
    setNow(performance.now());
    const left = lastOutput + ACTIVE_MS - performance.now();
    if (left <= 0) return;
    const timer = window.setTimeout(() => setNow(performance.now()), left + 50);
    return () => window.clearTimeout(timer);
  }, [lastOutput]);

  const active = lastOutput > 0 && now - lastOutput < ACTIVE_MS;
  const busy = running === true || active;
  const failed = running !== true && lastExit !== null && lastExit !== 0;

  const status =
    progress.state === "indeterminate"
      ? m.terminal_status_running()
      : progress.state !== "none"
        ? m.terminal_status_progress({ percent: progress.value })
        : running
          ? m.terminal_status_running()
          : active
            ? m.terminal_status_output()
            : failed
              ? m.terminal_status_exit({ code: lastExit ?? 0 })
              : bell
                ? m.terminal_status_bell()
                : unread
                  ? m.terminal_status_new_output()
                  : m.terminal_status_idle();

  const label = title || m.guest_terminal();

  return (
    <button
      type="button"
      title={`${m.terminal_dock_open()}: ${label}`}
      aria-label={`${m.terminal_dock_open()}: ${label}, ${status}`}
      onMouseDown={e => e.stopPropagation()}
      onMouseUp={e => e.stopPropagation()}
      onKeyDown={e => e.stopPropagation()}
      onKeyUp={e => e.stopPropagation()}
      onClick={e => {
        e.stopPropagation();
        setTerminalType("guest");
      }}
      className={cx(
        "fixed right-4 bottom-12 z-40 flex max-w-72 animate-fadeIn items-center gap-x-2.5 rounded-full",
        "border border-white/10 bg-slate-900/90 py-1 pr-4 pl-1 text-left text-slate-100 shadow-xl backdrop-blur",
        "transition-colors hover:bg-slate-800",
      )}
    >
      <span className="relative flex h-9 w-9 shrink-0 items-center justify-center">
        <Ring progress={progress} busy={busy} />
        <LuTerminal className="h-4 w-4" />
        {(failed || bell || unread) && (
          <span
            className={cx(
              "absolute top-0 right-0 h-2.5 w-2.5 rounded-full border-2 border-slate-900",
              failed ? "bg-red-500" : bell ? "bg-amber-400" : "bg-blue-400",
            )}
          />
        )}
      </span>
      <span className="min-w-0">
        <span className="block truncate text-xs font-semibold">{label}</span>
        <span className="flex items-center gap-x-1 text-[11px] text-slate-400">
          {bell && <LuBell className="h-3 w-3 text-amber-400" />}
          {status}
        </span>
      </span>
    </button>
  );
}
