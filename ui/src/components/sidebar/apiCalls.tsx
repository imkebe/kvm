import { useEffect, useMemo, useState } from "react";
import { LuChevronRight, LuRefreshCw } from "react-icons/lu";

import { cx } from "@/cva.config";
import { Button } from "@components/Button";
import SidebarHeader from "@components/SidebarHeader";
import { useUiStore } from "@hooks/stores";
import {
  ApiCallEntry,
  ApiCallLog,
  ApiCallSource,
  apiCallCounts,
  useApiCallStore,
} from "@hooks/useApiCallStore";
import { useJsonRpc, type JsonRpcResponse } from "@hooks/useJsonRpc";
import { m } from "@localizations/messages.js";

type Filter = "all" | ApiCallSource;

const SOURCE_STYLE: Record<ApiCallSource, string> = {
  mcp: "bg-blue-100 text-blue-800 dark:bg-blue-900/50 dark:text-blue-200",
  api: "bg-slate-200 text-slate-700 dark:bg-slate-700 dark:text-slate-200",
  terminal: "bg-amber-100 text-amber-800 dark:bg-amber-900/40 dark:text-amber-200",
};

function sourceLabel(source: ApiCallSource) {
  return source === "mcp"
    ? m.mcp_usage_source_mcp()
    : source === "api"
      ? m.mcp_usage_source_api()
      : m.mcp_usage_source_terminal();
}

function clock(ms: number) {
  return new Date(ms).toLocaleTimeString([], { hour12: false });
}

function duration(ms: number) {
  if (ms >= 60_000) return `${(ms / 60_000).toFixed(1)} min`;
  if (ms >= 1000) return `${(ms / 1000).toFixed(1)} s`;
  return `${ms.toFixed(ms < 10 ? 1 : 0)} ms`;
}

function CallRow({ entry }: { entry: ApiCallEntry }) {
  const [open, setOpen] = useState(false);
  const details = !!(entry.args || entry.error || entry.token || entry.remote);
  return (
    <li className="border-b border-slate-800/5 dark:border-slate-300/10">
      <button
        type="button"
        disabled={!details}
        onClick={() => setOpen(!open)}
        className="flex w-full items-center gap-2 px-3 py-1.5 text-left text-xs hover:bg-slate-50 disabled:cursor-default disabled:hover:bg-transparent dark:hover:bg-slate-800/60"
      >
        <LuChevronRight
          className={cx(
            "h-3 w-3 shrink-0 text-slate-400 transition-transform",
            open && "rotate-90",
            !details && "invisible",
          )}
        />
        <span className="w-16 shrink-0 font-mono text-slate-500 tabular-nums">
          {clock(entry.at)}
        </span>
        <span
          className={cx(
            "shrink-0 rounded px-1 py-px text-[10px] font-semibold",
            SOURCE_STYLE[entry.source],
          )}
        >
          {entry.via_mcp && entry.source !== "mcp"
            ? `${sourceLabel(entry.source)} · MCP`
            : sourceLabel(entry.source)}
        </span>
        <span className="min-w-0 flex-1 truncate font-mono text-slate-900 dark:text-slate-100">
          {entry.name}
        </span>
        <span className="shrink-0 text-slate-500 tabular-nums">{duration(entry.duration_ms)}</span>
        <span
          className={cx("h-2 w-2 shrink-0 rounded-full", entry.ok ? "bg-green-500" : "bg-red-500")}
          title={entry.ok ? m.mcp_log_ok() : m.mcp_log_failed()}
        />
      </button>
      {open && (
        <dl className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 bg-slate-50 px-3 py-2 pl-8 text-[11px] dark:bg-slate-800/50">
          {entry.args && (
            <>
              <dt className="text-slate-500">{m.mcp_log_arguments()}</dt>
              <dd className="font-mono break-all">{entry.args}</dd>
            </>
          )}
          {entry.error && (
            <>
              <dt className="text-slate-500">{m.mcp_log_error()}</dt>
              <dd className="font-mono break-all text-red-600 dark:text-red-400">{entry.error}</dd>
            </>
          )}
          {entry.token && (
            <>
              <dt className="text-slate-500">{m.mcp_log_token()}</dt>
              <dd>{entry.token}</dd>
            </>
          )}
          {entry.remote && (
            <>
              <dt className="text-slate-500">{m.mcp_log_remote()}</dt>
              <dd className="font-mono">{entry.remote}</dd>
            </>
          )}
        </dl>
      )}
    </li>
  );
}

// The API call log: every MCP tool call, device API call and terminal API
// shell since the KVM started (the last 300), live, with counts per name.
export default function ApiCallsSidebar() {
  const { send } = useJsonRpc();
  const setSidebarView = useUiStore(state => state.setSidebarView);
  const entries = useApiCallStore(state => state.entries);
  const counts = useApiCallStore(state => state.counts);
  const started = useApiCallStore(state => state.started);
  const load = useApiCallStore(state => state.load);
  const [filter, setFilter] = useState<Filter>("all");
  const [hideNested, setHideNested] = useState(true);
  const [loading, setLoading] = useState(false);

  const refresh = () => {
    setLoading(true);
    send("getApiCallLog", {}, (resp: JsonRpcResponse) => {
      setLoading(false);
      if (!("error" in resp)) load(resp.result as ApiCallLog);
    });
  };
  // eslint-disable-next-line react-hooks/exhaustive-deps
  useEffect(refresh, []);

  const shown = useMemo(
    () =>
      entries
        .filter(entry => filter === "all" || entry.source === filter)
        // An MCP tool's own device API calls repeat the tool call.
        .filter(entry => !(hideNested && entry.via_mcp && filter === "all"))
        .slice()
        .reverse(),
    [entries, filter, hideNested],
  );
  const perName = apiCallCounts(counts).filter(
    count => filter === "all" || count.source === filter,
  );

  const filters: [Filter, string][] = [
    ["all", m.mcp_log_all()],
    ["mcp", m.mcp_usage_source_mcp()],
    ["api", m.mcp_usage_source_api()],
    ["terminal", m.mcp_usage_source_terminal()],
  ];

  return (
    <div className="grid h-full grid-rows-(--grid-headerBody) shadow-xs">
      <SidebarHeader title={m.mcp_log_title()} setSidebarView={setSidebarView} />
      <div className="h-full space-y-3 overflow-y-auto bg-white px-4 py-3 pb-8 dark:bg-slate-900">
        <div className="flex items-center gap-2">
          <div className="flex flex-1 gap-1 rounded-md bg-slate-100 p-1 dark:bg-slate-800">
            {filters.map(([value, label]) => (
              <button
                key={value}
                type="button"
                onClick={() => setFilter(value)}
                className={cx(
                  "flex-1 rounded px-2 py-1 text-xs font-medium transition-colors",
                  filter === value
                    ? "bg-white text-slate-950 shadow-sm dark:bg-slate-700 dark:text-white"
                    : "text-slate-600 hover:text-slate-900 dark:text-slate-400 dark:hover:text-white",
                )}
              >
                {label}
              </button>
            ))}
          </div>
          <Button
            size="XS"
            theme="light"
            LeadingIcon={LuRefreshCw}
            text={m.network_discovery_refresh()}
            loading={loading}
            onClick={refresh}
          />
        </div>
        {filter === "all" && (
          <label className="flex items-center gap-2 text-xs text-slate-600 dark:text-slate-400">
            <input
              type="checkbox"
              checked={hideNested}
              onChange={event => setHideNested(event.target.checked)}
            />
            {m.mcp_log_hide_nested()}
          </label>
        )}

        <section className="space-y-1.5">
          <h3 className="text-xs font-semibold text-slate-700 dark:text-slate-300">
            {m.mcp_log_counts({ since: started ? clock(started) : "…" })}
          </h3>
          {perName.length === 0 ? (
            <p className="text-xs text-slate-500">{m.mcp_usage_no_calls()}</p>
          ) : (
            <table className="w-full text-left text-xs">
              <thead className="text-slate-500">
                <tr>
                  <th className="py-1 font-medium">{m.mcp_log_name()}</th>
                  <th className="py-1 text-right font-medium">{m.mcp_log_calls()}</th>
                  <th className="py-1 text-right font-medium">{m.mcp_log_errors()}</th>
                  <th className="py-1 text-right font-medium">{m.mcp_log_average()}</th>
                </tr>
              </thead>
              <tbody className="tabular-nums">
                {perName.map(count => (
                  <tr
                    key={`${count.source}/${count.name}`}
                    className="border-t border-slate-800/5 dark:border-slate-300/10"
                  >
                    <td className="truncate py-1 font-mono">
                      {count.name}{" "}
                      <span className="text-[10px] text-slate-500 uppercase">{count.source}</span>
                    </td>
                    <td className="py-1 text-right">{count.calls}</td>
                    <td
                      className={cx(
                        "py-1 text-right",
                        count.errors > 0 && "text-red-600 dark:text-red-400",
                      )}
                    >
                      {count.errors}
                    </td>
                    <td className="py-1 text-right">
                      {duration(count.calls ? count.total_ms / count.calls : 0)}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </section>

        <section className="space-y-1.5">
          <h3 className="text-xs font-semibold text-slate-700 dark:text-slate-300">
            {m.mcp_log_recent()}
          </h3>
          {shown.length === 0 ? (
            <p className="text-xs text-slate-500">{m.mcp_usage_no_calls()}</p>
          ) : (
            <ul className="overflow-hidden rounded border border-slate-800/10 dark:border-slate-300/20">
              {shown.map(entry => (
                <CallRow key={entry.id} entry={entry} />
              ))}
            </ul>
          )}
        </section>
      </div>
    </div>
  );
}
