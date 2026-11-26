import { useEffect, useState } from "react";
import {
  CartesianGrid,
  Cell,
  Line,
  LineChart,
  Pie,
  PieChart,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";

import { GridCard } from "@components/Card";
import { useJsonRpc, type JsonRpcResponse } from "@hooks/useJsonRpc";
import { m } from "@localizations/messages.js";

interface DeviceStats {
  at: number;
  cpu_percent: number;
  temperature_c: number;
  mem_total_kb: number;
  mem_available_kb: number;
  mem_cached_kb: number;
  load1: number;
  uptime_sec: number;
}

const HISTORY = 60;
const CPU_COLOR = "#2563eb";
const TEMP_COLOR = "#ea580c";
const MEM_COLORS = ["#16a34a", "#86efac", "#cbd5e1"];

function megabytes(kb: number) {
  return `${Math.round(kb / 1024)} MB`;
}

export function ago(ms: number) {
  if (!ms) return m.device_status_never();
  const seconds = Math.max(0, Math.round((Date.now() - ms) / 1000));
  if (seconds < 60) return m.device_status_seconds_ago({ seconds });
  const minutes = Math.round(seconds / 60);
  if (minutes < 60) return m.device_status_minutes_ago({ minutes });
  return m.device_status_hours_ago({ hours: Math.round(minutes / 60) });
}

function uptime(seconds: number) {
  const h = Math.floor(seconds / 3600);
  const min = Math.floor((seconds % 3600) / 60);
  return h > 0 ? `${h} h ${min} min` : `${min} min`;
}

export function Badge({ on, onText, offText }: { on: boolean; onText: string; offText: string }) {
  return (
    <span
      className={
        "rounded px-1.5 py-0.5 text-[11px] font-semibold " +
        (on
          ? "bg-green-100 text-green-800 dark:bg-green-900/50 dark:text-green-200"
          : "bg-slate-200 text-slate-600 dark:bg-slate-700 dark:text-slate-300")
      }
    >
      {on ? onText : offText}
    </span>
  );
}

export function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section className="space-y-2 rounded-md border border-slate-800/10 p-3 dark:border-slate-300/20">
      <h3 className="text-sm font-semibold">{title}</h3>
      {children}
    </section>
  );
}

// The KVM's own processor, temperature and memory (the API use is on the
// MCP card, McpUsagePopover). It asks
// the KVM only while it is open: closing the popover unmounts it.
export default function DeviceStatusPopover() {
  const { send } = useJsonRpc();
  const [history, setHistory] = useState<DeviceStats[]>([]);

  useEffect(() => {
    let stopped = false;
    const poll = () =>
      send("getDeviceStats", {}, (resp: JsonRpcResponse) => {
        if (stopped || "error" in resp) return;
        const sample = resp.result as DeviceStats;
        setHistory(previous => [...previous.slice(-(HISTORY - 1)), sample]);
      });
    poll();
    const timer = window.setInterval(poll, 1000);
    return () => {
      stopped = true;
      window.clearInterval(timer);
    };
  }, [send]);

  const latest = history.at(-1);
  // The first sample counts since boot; the chart starts with the second.
  const chart = history.slice(1).map(sample => ({
    t: new Date(sample.at).toLocaleTimeString([], { minute: "2-digit", second: "2-digit" }),
    cpu: Math.round(sample.cpu_percent * 10) / 10,
    temp: sample.temperature_c,
  }));
  const used = latest ? latest.mem_total_kb - latest.mem_available_kb : 0;
  const cache = latest ? Math.min(latest.mem_cached_kb, latest.mem_available_kb) : 0;
  const free = latest ? Math.max(0, latest.mem_available_kb - cache) : 0;
  const memory = [
    { name: m.device_status_mem_used(), value: used },
    { name: m.device_status_mem_cache(), value: cache },
    { name: m.device_status_mem_free(), value: free },
  ];

  return (
    <GridCard cardClassName="border border-solid border-slate-800/30 shadow-lg outline-none dark:border-slate-300/30">
      <div className="max-h-[calc(100dvh-150px)] space-y-3 overflow-y-auto p-4 text-slate-900 dark:text-slate-100">
        <div className="flex items-baseline justify-between">
          <h2 className="text-base font-semibold">{m.device_status_title()}</h2>
          {latest && (
            <span className="text-xs text-slate-500">
              {m.device_status_uptime({ time: uptime(latest.uptime_sec) })}
            </span>
          )}
        </div>

        <Section title={m.device_status_cpu_title()}>
          <div className="flex gap-4 text-xs">
            <span style={{ color: CPU_COLOR }}>
              {m.device_status_cpu({ value: latest ? latest.cpu_percent.toFixed(0) : "–" })}
            </span>
            <span style={{ color: TEMP_COLOR }}>
              {m.device_status_temp({ value: latest ? String(latest.temperature_c) : "–" })}
            </span>
            <span className="text-slate-500">
              {m.device_status_load({ value: latest ? latest.load1.toFixed(2) : "–" })}
            </span>
          </div>
          <div className="h-32">
            <ResponsiveContainer width="100%" height="100%">
              <LineChart data={chart} margin={{ top: 4, right: 0, bottom: 0, left: -20 }}>
                <CartesianGrid strokeDasharray="3 3" stroke="#94a3b833" />
                <XAxis dataKey="t" hide />
                <YAxis yAxisId="cpu" domain={[0, 100]} tick={{ fontSize: 10 }} unit="%" />
                <YAxis
                  yAxisId="temp"
                  orientation="right"
                  domain={["dataMin - 5", "dataMax + 5"]}
                  tick={{ fontSize: 10 }}
                  unit="°"
                  width={34}
                />
                <Tooltip contentStyle={{ fontSize: 12 }} />
                <Line
                  yAxisId="cpu"
                  type="monotone"
                  dataKey="cpu"
                  name={m.device_status_cpu_series()}
                  stroke={CPU_COLOR}
                  dot={false}
                  isAnimationActive={false}
                  unit="%"
                />
                <Line
                  yAxisId="temp"
                  type="monotone"
                  dataKey="temp"
                  name={m.device_status_temp_series()}
                  stroke={TEMP_COLOR}
                  dot={false}
                  isAnimationActive={false}
                  unit=" °C"
                />
              </LineChart>
            </ResponsiveContainer>
          </div>
        </Section>

        <Section title={m.device_status_memory_title()}>
          <div className="flex items-center gap-4">
            <div className="h-28 w-28 shrink-0">
              <ResponsiveContainer width="100%" height="100%">
                <PieChart>
                  <Pie
                    data={memory}
                    dataKey="value"
                    innerRadius={28}
                    outerRadius={50}
                    isAnimationActive={false}
                    stroke="none"
                  >
                    {memory.map((entry, i) => (
                      <Cell key={entry.name} fill={MEM_COLORS[i]} />
                    ))}
                  </Pie>
                  <Tooltip formatter={value => megabytes(Number(value))} />
                </PieChart>
              </ResponsiveContainer>
            </div>
            <ul className="space-y-1 text-xs">
              {memory.map((entry, i) => (
                <li key={entry.name} className="flex items-center gap-2">
                  <span
                    className="inline-block h-2.5 w-2.5 rounded-sm"
                    style={{ background: MEM_COLORS[i] }}
                  />
                  {entry.name}: {megabytes(entry.value)}
                </li>
              ))}
              {latest && (
                <li className="text-slate-500">
                  {m.device_status_mem_total({ total: megabytes(latest.mem_total_kb) })}
                </li>
              )}
            </ul>
          </div>
        </Section>
      </div>
    </GridCard>
  );
}
