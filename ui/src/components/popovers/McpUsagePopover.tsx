import { useEffect, useState } from "react";
import { CloseButton } from "@headlessui/react";
import { LuBot, LuListTree, LuRotateCcw } from "react-icons/lu";

import { GridCard } from "@components/Card";
import { Button } from "@components/Button";
import { Badge, Section, ago } from "@components/popovers/DeviceStatusPopover";
import { useUiStore } from "@hooks/stores";
import { ApiCallLog, apiCallCounts, useApiCallStore } from "@hooks/useApiCallStore";
import { useJsonRpc, type JsonRpcResponse } from "@hooks/useJsonRpc";
import { useDeviceUiNavigation } from "@hooks/useAppNavigation";
import { m } from "@localizations/messages.js";

export interface McpActivity {
  mcp_enabled: boolean;
  mcp_sessions: number;
  device_api_enabled: boolean;
  terminal_api_enabled: boolean;
  terminal_ready: boolean;
  tokens: number;
  tokens_expired: number;
  tokens_active: number;
  last_token_use: number;
  last_token_name?: string;
  device_api_calls: number;
  device_api_last_call: number;
  api_shells: number;
  shells: number;
}

// The MCP server and API switches, tokens and shells, read every interval.
export function useMcpActivity(intervalMs: number) {
  const { send } = useJsonRpc();
  const [mcp, setMcp] = useState<McpActivity | null>(null);
  useEffect(() => {
    let stopped = false;
    const poll = () =>
      send("getMcpActivity", {}, (resp: JsonRpcResponse) => {
        if (!stopped && !("error" in resp)) setMcp(resp.result as McpActivity);
      });
    poll();
    // A background tab does not poll; it polls at once when shown again.
    const onVisible = () => !document.hidden && poll();
    const timer = window.setInterval(onVisible, intervalMs);
    document.addEventListener("visibilitychange", onVisible);
    return () => {
      stopped = true;
      window.clearInterval(timer);
      document.removeEventListener("visibilitychange", onVisible);
    };
  }, [send, intervalMs]);
  return mcp;
}

// The MCP card's panel: the MCP server and the APIs it uses, what programs
// did since the KVM started, and the way to the call log.
export default function McpUsagePopover() {
  const { navigateTo } = useDeviceUiNavigation();
  const mcp = useMcpActivity(3000);
  const totals = useApiCallStore(state => state.totals);
  const counts = useApiCallStore(state => state.counts);
  const setSidebarView = useUiStore(state => state.setSidebarView);
  const replace = useApiCallStore(state => state.replace);
  const { send } = useJsonRpc();
  const top = apiCallCounts(counts).slice(0, 6);

  const reset = () => {
    if (!window.confirm(m.mcp_usage_reset_confirm())) return;
    send("resetApiCallLog", {}, (resp: JsonRpcResponse) => {
      if (!("error" in resp)) replace(resp.result as ApiCallLog);
    });
  };

  return (
    <GridCard>
      <div className="max-h-[calc(100dvh-150px)] space-y-3 overflow-y-auto p-4 text-slate-900 dark:text-slate-100">
        <Section title={m.mcp_usage_server_title()}>
          {!mcp ? (
            <p className="text-xs text-slate-500">…</p>
          ) : (
            <div className="space-y-2 text-xs">
              <div className="flex flex-wrap items-center gap-2">
                <span>{m.mcp_usage_server()}</span>
                <Badge
                  on={mcp.mcp_enabled}
                  onText={m.device_status_on()}
                  offText={m.device_status_off()}
                />
                <span className="ml-2">{m.mcp_scope_device()}</span>
                <Badge
                  on={mcp.device_api_enabled}
                  onText={m.device_status_on()}
                  offText={m.device_status_off()}
                />
                <span className="ml-2">{m.mcp_scope_terminal()}</span>
                <Badge
                  on={mcp.terminal_api_enabled}
                  onText={mcp.terminal_ready ? m.device_status_ready() : m.device_status_on()}
                  offText={m.device_status_off()}
                />
              </div>
              <dl className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1">
                <dt className="text-slate-500">{m.mcp_usage_sessions()}</dt>
                <dd>{mcp.mcp_sessions}</dd>
                <dt className="text-slate-500">{m.device_status_tokens()}</dt>
                <dd>
                  {m.device_status_tokens_value({
                    total: mcp.tokens,
                    active: mcp.tokens_active,
                    expired: mcp.tokens_expired,
                  })}
                </dd>
                <dt className="text-slate-500">{m.device_status_last_token()}</dt>
                <dd>
                  {mcp.last_token_use
                    ? `${mcp.last_token_name ?? ""} · ${ago(mcp.last_token_use * 1000)}`
                    : m.device_status_never()}
                </dd>
                <dt className="text-slate-500">{m.device_status_api_calls()}</dt>
                <dd>
                  {m.device_status_api_calls_value({
                    count: mcp.device_api_calls,
                    last: ago(mcp.device_api_last_call),
                  })}
                </dd>
                <dt className="text-slate-500">{m.device_status_shells()}</dt>
                <dd>{m.device_status_shells_value({ api: mcp.api_shells, total: mcp.shells })}</dd>
              </dl>
            </div>
          )}
        </Section>

        <Section title={m.mcp_usage_calls_title()}>
          <div className="grid grid-cols-3 gap-2 text-center text-xs">
            {(
              [
                ["mcp", m.mcp_usage_source_mcp()],
                ["api", m.mcp_usage_source_api()],
                ["terminal", m.mcp_usage_source_terminal()],
              ] as const
            ).map(([source, label]) => (
              <div
                key={source}
                className="rounded border border-slate-800/10 px-2 py-1.5 dark:border-slate-300/20"
              >
                <div className="text-base font-semibold tabular-nums">{totals[source] ?? 0}</div>
                <div className="text-slate-500">{label}</div>
              </div>
            ))}
          </div>
          {top.length === 0 ? (
            <p className="text-xs text-slate-500">{m.mcp_usage_no_calls()}</p>
          ) : (
            <ul className="space-y-1 text-xs">
              {top.map(count => (
                <li key={`${count.source}/${count.name}`} className="flex items-center gap-2">
                  <span className="truncate font-mono">{count.name}</span>
                  <span className="text-[10px] text-slate-500 uppercase">{count.source}</span>
                  <span className="ml-auto tabular-nums">
                    {count.calls}
                    {count.errors > 0 && (
                      <span className="ml-1 text-red-600 dark:text-red-400">
                        ({m.mcp_usage_errors({ count: count.errors })})
                      </span>
                    )}
                  </span>
                </li>
              ))}
            </ul>
          )}
        </Section>

        <div className="flex flex-wrap justify-end gap-2">
          <Button
            size="XS"
            theme="light"
            LeadingIcon={LuRotateCcw}
            text={m.mcp_usage_reset()}
            className="mr-auto"
            onClick={reset}
          />
          <CloseButton
            as={Button}
            size="XS"
            theme="light"
            LeadingIcon={LuBot}
            text={m.device_status_open_mcp()}
            onClick={() => navigateTo("/settings/mcp")}
          />
          <CloseButton
            as={Button}
            size="XS"
            theme="primary"
            LeadingIcon={LuListTree}
            text={m.mcp_usage_call_log()}
            onClick={() => setSidebarView("api-calls")}
          />
        </div>
      </div>
    </GridCard>
  );
}
