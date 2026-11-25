import { useEffect, useState } from "react";
import { Popover, PopoverButton, PopoverPanel } from "@headlessui/react";
import { LuBot, LuChartColumn } from "react-icons/lu";

import { cx } from "@/cva.config";
import StatusCard from "@components/StatusCards";
import McpUsagePopover, { useMcpActivity } from "@components/popovers/McpUsagePopover";
import { useUiStore } from "@hooks/stores";
import { ApiCallLog, useApiCallStore } from "@hooks/useApiCallStore";
import { useJsonRpc, type JsonRpcResponse } from "@hooks/useJsonRpc";
import { m } from "@localizations/messages.js";

// A call this recent shows on the card by name.
const RECENT_MS = 3000;

// While the panel is open, keys go to it rather than to the video.
function Panel() {
  const setDisableVideoFocusTrap = useUiStore(state => state.setDisableVideoFocusTrap);
  useEffect(() => {
    setDisableVideoFocusTrap(true);
    return () => setDisableVideoFocusTrap(false);
  }, [setDisableVideoFocusTrap]);
  return <McpUsagePopover />;
}

function UsageButton() {
  return (
    <Popover>
      <PopoverButton
        title={m.mcp_usage_view()}
        aria-label={m.mcp_usage_view()}
        className="flex h-7 w-7 items-center justify-center rounded-md bg-blue-700 text-white shadow-sm transition-colors hover:bg-blue-800 focus:outline-none focus-visible:ring-2 focus-visible:ring-blue-400 data-open:bg-blue-900 dark:bg-blue-600 dark:hover:bg-blue-500"
      >
        <LuChartColumn className="h-4 w-4" />
      </PopoverButton>
      <PopoverPanel
        anchor="bottom end"
        transition
        className={cx(
          "z-30 flex w-[min(440px,calc(100vw-24px))] origin-top flex-col overflow-visible! [--anchor-gap:8px]",
          "transition duration-300 ease-out data-closed:translate-y-4 data-closed:opacity-0",
        )}
      >
        <Panel />
      </PopoverPanel>
    </Popover>
  );
}

// The light of the MCP card: green while the server is on, a ring that
// pulses on each API call.
function ActivityDot({ on, pulse }: { on: boolean; pulse: number }) {
  const [lit, setLit] = useState(false);
  useEffect(() => {
    if (pulse === 0) return;
    setLit(true);
    const timer = window.setTimeout(() => setLit(false), 1200);
    return () => window.clearTimeout(timer);
  }, [pulse]);
  return (
    <span className="relative flex h-2 w-2 shrink-0">
      {lit && (
        <span
          key={pulse}
          className="absolute inline-flex h-full w-full animate-ping rounded-full bg-blue-500 opacity-75"
        />
      )}
      <span
        className={cx(
          "relative inline-flex h-2 w-2 rounded-full border transition-colors",
          lit
            ? "border-blue-600 bg-blue-500"
            : on
              ? "border-green-600 bg-green-500"
              : "border-slate-400 bg-slate-300 dark:border-slate-500 dark:bg-slate-600",
        )}
      />
    </span>
  );
}

// The fourth card of the top ribbon: the KVM's MCP server, live. It loads
// the call record once and then follows the "apiCall" events.
export default function McpStatusCard() {
  const { send } = useJsonRpc();
  const mcp = useMcpActivity(10_000);
  const pulse = useApiCallStore(state => state.pulse);
  const lastCallAt = useApiCallStore(state => state.lastCallAt);
  const last = useApiCallStore(state => state.entries.at(-1));
  const load = useApiCallStore(state => state.load);
  const [now, setNow] = useState(() => Date.now());

  useEffect(() => {
    send("getApiCallLog", {}, (resp: JsonRpcResponse) => {
      if (!("error" in resp)) load(resp.result as ApiCallLog);
    });
  }, [send, load]);

  // The last call's name fades back to the state.
  useEffect(() => {
    if (!lastCallAt) return;
    setNow(Date.now());
    const timer = window.setTimeout(() => setNow(Date.now()), RECENT_MS + 50);
    return () => window.clearTimeout(timer);
  }, [lastCallAt]);

  const on = !!mcp?.mcp_enabled;
  const recent = !!last && lastCallAt > 0 && now - lastCallAt < RECENT_MS;
  const status = recent
    ? last.name
    : !mcp
      ? "…"
      : !on
        ? m.device_status_off()
        : mcp.mcp_sessions > 0
          ? m.mcp_card_sessions({ count: mcp.mcp_sessions })
          : m.mcp_card_idle();

  return (
    <StatusCard
      title={m.mcp_card_title()}
      status={status}
      icon={({ className }) => (
        <LuBot className={cx(className, "h-5 w-5 text-slate-700 dark:text-slate-300")} />
      )}
      indicator={<ActivityDot on={on} pulse={pulse} />}
      action={<UsageButton />}
    />
  );
}
