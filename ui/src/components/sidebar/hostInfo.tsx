import { memo, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { LuChevronDown, LuChevronRight, LuRefreshCw } from "react-icons/lu";

import { m } from "@localizations/messages.js";
import { cx } from "@/cva.config";
import { useUiStore } from "@hooks/stores";
import { useDeviceRpc } from "@hooks/useDeviceRpc";
import {
  buildProcessTree,
  flattenProcessTree,
  formatBytes,
  formatPercent,
  formatRate,
  formatUptime,
  formatUsbId,
  gpuMetricsIndex,
  HostHistorySample,
  HostInfoSections,
  HostInventory,
  HostMetrics,
  HostProcess,
  HostProcessesResult,
  HostUsbDevice,
  HostUsbResult,
  historyChartData,
  loadProcessCadence,
  PROCESS_CADENCES_MS,
  ProcessHistory,
  processCpu,
  processKey,
  saveProcessCadence,
  startSnapshotLoop,
  useHostInfoStore,
  useHostInfoWatch,
  usedPercent,
} from "@hooks/hostInfo";
import { Metric } from "@components/Metric";
import { SettingsSectionHeader } from "@components/SettingsSectionHeader";
import SidebarHeader from "@components/SidebarHeader";
import { GridCard } from "@components/Card";
import { Button } from "@components/Button";
import InputField from "@components/InputField";

type Tab = "overview" | "storage" | "network" | "processes" | "usb";

// Whether the agent provides what a tab shows.
function tabSupported(tab: Tab, sections: HostInfoSections) {
  switch (tab) {
    case "overview":
    case "processes":
      return !!(
        sections.inventory ||
        sections.metrics ||
        (tab === "processes" && sections.processes)
      );
    case "storage":
    case "network":
      return !!sections.inventory;
    case "usb":
      return !!sections.usb;
  }
}

function tabLabel(tab: Tab) {
  switch (tab) {
    case "overview":
      return m.host_info_tab_overview();
    case "storage":
      return m.host_info_tab_storage();
    case "network":
      return m.host_info_tab_network();
    case "processes":
      return m.host_info_tab_processes();
    case "usb":
      return m.host_info_tab_usb();
  }
}

function errorText(error: unknown) {
  return error instanceof Error ? error.message : m.unknown_error();
}

function Message({ children, tone }: { children: React.ReactNode; tone?: "error" }) {
  return (
    <div
      className={cx(
        "rounded-sm border px-3 py-2 text-sm",
        tone === "error"
          ? "border-red-300 bg-red-50 text-red-800 dark:border-red-700 dark:bg-red-950 dark:text-red-200"
          : "border-slate-800/20 bg-slate-50 text-slate-700 dark:border-slate-300/20 dark:bg-slate-800 dark:text-slate-300",
      )}
    >
      {children}
    </div>
  );
}

function Row({ label, value }: { label: string; value: React.ReactNode }) {
  if (value === "" || value == null) return null;
  return (
    <div className="flex items-start justify-between gap-x-4 text-sm">
      <div className="shrink-0 text-slate-600 dark:text-slate-400">{label}</div>
      <div className="min-w-0 text-right break-words text-black dark:text-white">{value}</div>
    </div>
  );
}

function Bar({ percent, label }: { percent: number; label?: string }) {
  const clamped = Math.min(100, Math.max(0, percent));
  return (
    <div
      className="h-2 w-full overflow-hidden rounded-full bg-slate-200 dark:bg-slate-700"
      role="progressbar"
      aria-valuemin={0}
      aria-valuemax={100}
      aria-valuenow={Math.round(clamped)}
      aria-label={label}
    >
      <div
        className={cx(
          "h-full rounded-full",
          clamped >= 90 ? "bg-red-500" : clamped >= 75 ? "bg-amber-500" : "bg-blue-500",
        )}
        style={{ width: `${clamped}%` }}
      />
    </div>
  );
}

function UsageRow({
  label,
  used,
  total,
  detail,
}: {
  label: string;
  used: number;
  total: number;
  detail?: string;
}) {
  const percent = usedPercent(used, total);
  return (
    <div className="space-y-1">
      <div className="flex items-baseline justify-between gap-x-4 text-sm">
        <div className="min-w-0 truncate text-black dark:text-white" title={label}>
          {label}
          {detail && <span className="ml-2 text-xs text-slate-500">{detail}</span>}
        </div>
        <div className="shrink-0 text-xs text-slate-600 dark:text-slate-400">
          {formatBytes(used)} / {formatBytes(total)} ({formatPercent(percent, 0)})
        </div>
      </div>
      <Bar percent={percent} label={label} />
    </div>
  );
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div className="space-y-3">
      <SettingsSectionHeader title={title} />
      {children}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Overview

function OverviewTab({
  inventory,
  metrics,
  sections,
}: {
  inventory: HostInventory | null | undefined;
  metrics: HostMetrics | null;
  sections: HostInfoSections;
}) {
  const history = useHostInfoStore(state => state.history);
  const os = inventory?.os;
  const cpu = inventory?.cpu;
  const load = metrics?.load ?? [];
  const showLoad = load.some(v => v > 0);
  const perCore = metrics?.cpu?.per_core ?? [];
  const temps = metrics?.temps ?? [];
  const memory = metrics?.memory;

  const cpuData = useMemo(() => historyChartData(history, s => s.cpu), [history]);
  const memData = useMemo(() => historyChartData(history, s => s.memory), [history]);

  const osName = [os?.name, os?.version].filter(Boolean).join(" ");
  const cores = cpu
    ? [
        cpu.physical_cores ? m.host_info_cores_physical({ count: cpu.physical_cores }) : "",
        cpu.logical_cores ? m.host_info_cores_logical({ count: cpu.logical_cores }) : "",
      ]
        .filter(Boolean)
        .join(", ")
    : "";

  // Metrics-based chart data only moves when a new sample arrives.
  const waiting = !metrics;

  return (
    <div className="space-y-8">
      {sections.inventory && (
        <Section title={m.host_info_system()}>
          <div className="space-y-1.5">
            <Row label={m.host_info_os()} value={osName} />
            <Row label={m.host_info_kernel()} value={os?.kernel} />
            <Row label={m.host_info_arch()} value={os?.arch} />
            <Row label={m.host_info_hostname()} value={os?.hostname} />
            <Row
              label={m.host_info_uptime()}
              value={metrics?.uptime_s ? formatUptime(metrics.uptime_s) : ""}
            />
            <Row
              label={m.host_info_tab_processes()}
              value={metrics?.process_count ? String(metrics.process_count) : ""}
            />
            <Row label={m.host_info_cpu()} value={cpu?.brand} />
            <Row label={m.host_info_cores()} value={cores} />
            <Row
              label={m.host_info_frequency()}
              value={cpu?.frequency_mhz ? `${cpu.frequency_mhz} MHz` : ""}
            />
            <Row
              label={m.host_info_memory_total()}
              value={inventory?.memory?.total ? formatBytes(inventory.memory.total) : ""}
            />
          </div>
        </Section>
      )}

      {sections.metrics ? (
        <>
          <Section title={m.host_info_processor()}>
            <Metric
              title={m.host_info_cpu_usage()}
              description={
                metrics
                  ? m.host_info_cpu_usage_now({ value: formatPercent(metrics.cpu?.total) })
                  : m.host_info_waiting()
              }
              data={cpuData}
              gate={waiting ? new Map() : undefined}
              domain={[0, 100]}
              unit="%"
            />
            {perCore.length > 0 && (
              <div className="space-y-1.5">
                <div className="text-sm font-semibold text-black dark:text-white">
                  {m.host_info_per_core()}
                </div>
                <div className="grid grid-cols-2 gap-x-4 gap-y-1.5">
                  {perCore.map((value, index) => (
                    <div key={index} className="flex items-center gap-x-2">
                      <div className="w-6 shrink-0 text-xs text-slate-500">{index}</div>
                      <Bar percent={value} label={`${index}`} />
                      <div className="w-10 shrink-0 text-right text-xs text-slate-600 dark:text-slate-400">
                        {formatPercent(value, 0)}
                      </div>
                    </div>
                  ))}
                </div>
              </div>
            )}
            {showLoad && (
              <Row
                label={m.host_info_load_average()}
                value={load.map(v => v.toFixed(2)).join("  ")}
              />
            )}
            {temps.length > 0 && (
              <div className="space-y-1.5">
                <div className="text-sm font-semibold text-black dark:text-white">
                  {m.host_info_temperatures()}
                </div>
                {temps.map((t, index) => (
                  <Row
                    key={`${t.label}-${index}`}
                    label={t.label || "–"}
                    value={t.c != null ? `${t.c.toFixed(0)} °C` : "–"}
                  />
                ))}
              </div>
            )}
          </Section>

          <Section title={m.host_info_memory()}>
            <Metric
              title={m.host_info_memory_usage()}
              description={
                memory
                  ? `${formatBytes(memory.used)} / ${formatBytes(memory.total)}`
                  : m.host_info_waiting()
              }
              data={memData}
              gate={waiting ? new Map() : undefined}
              domain={[0, 100]}
              unit="%"
            />
            {memory && (memory.swap_total ?? 0) > 0 && (
              <UsageRow
                label={m.host_info_swap()}
                used={memory.swap_used ?? 0}
                total={memory.swap_total ?? 0}
              />
            )}
          </Section>
        </>
      ) : (
        <Message>{m.host_info_section_unsupported()}</Message>
      )}

      {sections.inventory && (inventory?.gpus?.length ?? 0) > 0 && (
        <Section title={m.host_info_gpus()}>
          <div className="space-y-6">
            {inventory?.gpus?.map((gpu, index) => (
              <GpuDetails
                key={`${gpu.name}-${index}`}
                gpu={gpu}
                index={gpuMetricsIndex(metrics, gpu.name, index)}
                metrics={metrics}
                history={history}
                showLive={!!sections.metrics}
              />
            ))}
          </div>
        </Section>
      )}
    </div>
  );
}

type HostGpu = NonNullable<HostInventory["gpus"]>[number];

const MIB = 1024 * 1024;

// A chart top for memory in MB: 25% above the highest value, rounded up to
// a power of two (at least 256 MB), so the scale does not jump every sample.
function memoryDomainMb(values: readonly (number | null)[]): [number, number] {
  const highest = Math.max(0, ...values.filter((v): v is number => v != null));
  let top = 256;
  while (top < highest * 1.25) top *= 2;
  return [0, top];
}

// One GPU: inventory rows, the live values as rows and, for each value the
// system reports, a live chart. A value the system never reported shows as
// "not reported" instead of an empty chart.
function GpuDetails({
  gpu,
  index,
  metrics,
  history,
  showLive,
}: {
  gpu: HostGpu;
  // Position in metrics.gpus and in each history sample's gpus.
  index: number;
  metrics: HostMetrics | null;
  history: readonly HostHistorySample[];
  showLive: boolean;
}) {
  const live = metrics?.gpus?.[index];
  const dedicated = gpu.vram ?? 0;
  const waiting = !metrics;

  const loadReported =
    live?.load != null || history.some(sample => sample.gpus[index]?.load != null);
  const memoryReported =
    live?.vram_used != null || history.some(sample => sample.gpus[index]?.memory != null);

  const loadData = useMemo(
    () => historyChartData(history, sample => sample.gpus[index]?.load),
    [history, index],
  );
  // Percent of the dedicated memory; MB for a GPU that shares the main memory.
  const memoryData = useMemo(
    () =>
      historyChartData(history, sample => {
        const used = sample.gpus[index]?.memory;
        if (used == null) return null;
        return dedicated > 0 ? Math.min(100, (used / dedicated) * 100) : used / MIB;
      }),
    [history, index, dedicated],
  );
  const memoryDomain = useMemo<[number, number]>(
    () => (dedicated > 0 ? [0, 100] : memoryDomainMb(memoryData.map(point => point.metric))),
    [dedicated, memoryData],
  );

  const notReported = m.host_info_gpu_not_reported();
  const memoryText =
    live?.vram_used == null
      ? notReported
      : dedicated > 0
        ? `${formatBytes(live.vram_used)} / ${formatBytes(dedicated)} (${formatPercent(usedPercent(live.vram_used, dedicated), 0)})`
        : m.host_info_gpu_memory_shared({ value: formatBytes(live.vram_used) });

  return (
    <div className="space-y-3">
      <div className="space-y-1">
        <div className="text-sm font-semibold text-black dark:text-white">
          {gpu.name || m.host_info_unknown()}
        </div>
        <Row label={m.host_info_vendor()} value={gpu.vendor} />
        <Row label={m.host_info_vram()} value={dedicated ? formatBytes(dedicated) : ""} />
        <Row label={m.host_info_driver()} value={gpu.driver} />
        {showLive && !waiting && (
          <>
            <Row
              label={m.host_info_gpu_load()}
              value={live?.load != null ? formatPercent(live.load) : notReported}
            />
            <Row label={m.host_info_vram_used()} value={memoryText} />
          </>
        )}
      </div>
      {showLive && (waiting || loadReported) && (
        <Metric
          title={m.host_info_gpu_load_chart()}
          description={
            waiting
              ? m.host_info_waiting()
              : m.host_info_cpu_usage_now({ value: formatPercent(live?.load) })
          }
          data={loadData}
          gate={waiting ? new Map() : undefined}
          domain={[0, 100]}
          unit="%"
        />
      )}
      {showLive && (waiting || memoryReported) && (
        <Metric
          title={m.host_info_gpu_memory_chart()}
          description={waiting ? m.host_info_waiting() : memoryText}
          data={memoryData}
          gate={waiting ? new Map() : undefined}
          domain={memoryDomain}
          unit={dedicated > 0 ? "%" : " MB"}
        />
      )}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Storage

function StorageTab({
  inventory,
  metrics,
}: {
  inventory: HostInventory | null | undefined;
  metrics: HostMetrics | null;
}) {
  const disks = inventory?.disks ?? [];
  if (disks.length === 0) return <Message>{m.host_info_no_disks()}</Message>;
  return (
    <div className="space-y-4">
      {disks.map((disk, index) => {
        // Live numbers win over the inventory's.
        const live = metrics?.disks?.find(d => d.mount === disk.mount);
        const total = live?.total ?? disk.total ?? 0;
        const available = live?.available ?? disk.available ?? 0;
        const detail = [disk.fs, disk.name, disk.removable ? m.host_info_removable() : ""]
          .filter(Boolean)
          .join(" · ");
        return (
          <UsageRow
            key={`${disk.mount}-${index}`}
            label={disk.mount || disk.name || "–"}
            used={Math.max(0, total - available)}
            total={total}
            detail={detail}
          />
        );
      })}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Network

function NetworkTab({
  inventory,
  metrics,
}: {
  inventory: HostInventory | null | undefined;
  metrics: HostMetrics | null;
}) {
  const interfaces = inventory?.networks ?? [];
  if (interfaces.length === 0) return <Message>{m.host_info_no_networks()}</Message>;
  return (
    <div className="space-y-3">
      {interfaces.map((net, index) => {
        const live = metrics?.networks?.find(n => n.name === net.name);
        return (
          <GridCard key={`${net.name}-${index}`}>
            <div className="space-y-1 p-3">
              <div className="flex items-center justify-between text-sm font-semibold text-black dark:text-white">
                <span className="truncate">{net.name || "–"}</span>
                <span
                  className={cx(
                    "text-xs font-medium",
                    net.up ? "text-green-600 dark:text-green-400" : "text-slate-500",
                  )}
                >
                  {net.up ? m.host_info_link_up() : m.host_info_link_down()}
                </span>
              </div>
              <Row label={m.host_info_mac()} value={net.mac} />
              {(net.addresses ?? []).map(address => (
                <Row key={address} label={m.host_info_address()} value={address} />
              ))}
              {live && (
                <>
                  <Row label={m.host_info_download()} value={formatRate(live.rx_bps)} />
                  <Row label={m.host_info_upload()} value={formatRate(live.tx_bps)} />
                  <Row label={m.host_info_received()} value={formatBytes(live.rx_total)} />
                  <Row label={m.host_info_sent()} value={formatBytes(live.tx_total)} />
                </>
              )}
            </div>
          </GridCard>
        );
      })}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Processes

const SPARK_WIDTH = 48;
const SPARK_HEIGHT = 14;

// Plain SVG trend line, scaled to max(100, largest value). Needs two points.
function Sparkline({ values }: { values: readonly number[] | undefined }) {
  if (!values || values.length < 2) {
    return <span className="inline-block shrink-0" style={{ width: SPARK_WIDTH }} aria-hidden />;
  }
  const max = Math.max(100, ...values);
  const last = values.length - 1;
  const points = values
    .map((v, i) => {
      const x = 1 + (i / last) * (SPARK_WIDTH - 2);
      const y = SPARK_HEIGHT - 1 - (Math.max(0, v) / max) * (SPARK_HEIGHT - 2);
      return `${x.toFixed(1)},${y.toFixed(1)}`;
    })
    .join(" ");
  return (
    <svg
      width={SPARK_WIDTH}
      height={SPARK_HEIGHT}
      viewBox={`0 0 ${SPARK_WIDTH} ${SPARK_HEIGHT}`}
      className="shrink-0 text-blue-500"
      aria-hidden
    >
      <polyline
        points={points}
        fill="none"
        stroke="currentColor"
        strokeWidth="1"
        strokeLinejoin="round"
      />
    </svg>
  );
}

const ProcessLine = memo(function ProcessLine({
  name,
  pid,
  user,
  cpu,
  trend,
  mem,
  depth = 0,
  toggle,
}: {
  name?: string;
  pid: number;
  user?: string;
  cpu: number | null;
  trend?: readonly number[];
  mem?: number;
  depth?: number;
  toggle?: React.ReactNode;
}) {
  return (
    <div className="flex items-center gap-x-2 border-b border-slate-800/10 py-1 text-xs dark:border-slate-300/10">
      <div
        className="flex min-w-0 grow items-center gap-x-1"
        style={{ paddingLeft: `${Math.min(depth, 12) * 12}px` }}
      >
        <span className="inline-flex h-4 w-4 shrink-0 items-center justify-center">{toggle}</span>
        <span className="truncate text-black dark:text-white" title={name}>
          {name || "–"}
        </span>
      </div>
      <div className="w-10 shrink-0 text-right text-slate-500">{pid}</div>
      {user !== undefined && (
        <div className="w-14 shrink-0 truncate text-right text-slate-500" title={user}>
          {user}
        </div>
      )}
      <div className="flex w-[6.5rem] shrink-0 items-center justify-end gap-x-1 text-slate-700 dark:text-slate-300">
        <Sparkline values={trend} />
        <span className="w-10 text-right">{cpu != null ? `${cpu.toFixed(1)}%` : "–"}</span>
      </div>
      <div className="w-14 shrink-0 text-right text-slate-700 dark:text-slate-300">
        {formatBytes(mem)}
      </div>
    </div>
  );
});

function ProcessHeader({ withUser, sinceStart }: { withUser: boolean; sinceStart: boolean }) {
  return (
    <div className="flex items-center gap-x-2 border-b border-slate-800/30 py-1 text-xs font-semibold text-slate-600 dark:border-slate-300/30 dark:text-slate-400">
      <div className="grow">{m.host_info_process_name()}</div>
      <div className="w-10 shrink-0 text-right">{m.host_info_process_pid()}</div>
      {withUser && <div className="w-14 shrink-0 text-right">{m.host_info_process_user()}</div>}
      <div className="w-[6.5rem] shrink-0 text-right">
        {sinceStart ? m.host_info_process_cpu_since_start() : m.host_info_process_cpu()}
      </div>
      <div className="w-14 shrink-0 text-right">{m.host_info_process_memory()}</div>
    </div>
  );
}

function trendOf(history: ProcessHistory, proc: HostProcess) {
  return history[processKey(proc)]?.cpu;
}

function TopProcesses({
  snapshot,
  history,
}: {
  snapshot: HostProcessesResult;
  history: ProcessHistory;
}) {
  const top = useMemo(() => {
    const scored = (snapshot.processes ?? []).map(proc => ({
      proc,
      cpu: processCpu(proc, snapshot),
    }));
    scored.sort(
      (a, b) =>
        (b.cpu ?? -1) - (a.cpu ?? -1) ||
        (b.proc.mem ?? 0) - (a.proc.mem ?? 0) ||
        a.proc.pid - b.proc.pid,
    );
    return scored.slice(0, 10);
  }, [snapshot]);

  return (
    <div>
      <ProcessHeader withUser={false} sinceStart={(snapshot.window_s ?? 0) <= 0} />
      {top.map(({ proc, cpu }) => (
        <ProcessLine
          key={processKey(proc)}
          name={proc.name}
          pid={proc.pid}
          cpu={cpu}
          trend={trendOf(history, proc)}
          mem={proc.mem}
        />
      ))}
    </div>
  );
}

function ProcessTree({
  snapshot,
  history,
  loading,
  disabled,
  onSnapshot,
}: {
  snapshot: HostProcessesResult;
  history: ProcessHistory;
  loading: boolean;
  disabled: boolean;
  onSnapshot: () => void;
}) {
  const processes = useMemo(() => snapshot.processes ?? [], [snapshot]);
  const roots = useMemo(
    () => buildProcessTree(processes, proc => processCpu(proc, snapshot) ?? -1),
    [processes, snapshot],
  );
  const [expanded, setExpanded] = useState<Set<number>>(new Set());
  const [filter, setFilter] = useState("");
  const { rows, truncated } = useMemo(
    () => flattenProcessTree(roots, expanded, filter),
    [roots, expanded, filter],
  );

  const toggle = (pid: number) =>
    setExpanded(prev => {
      const next = new Set(prev);
      if (!next.delete(pid)) next.add(pid);
      return next;
    });

  const expandAll = () => {
    const all = new Set<number>();
    const stack = [...roots];
    while (stack.length > 0) {
      const node = stack.pop();
      if (!node) break;
      if (node.children.length > 0) all.add(node.proc.pid);
      stack.push(...node.children);
    }
    setExpanded(all);
  };

  return (
    <div className="space-y-2">
      <div className="flex items-center gap-x-2">
        <div className="grow">
          <InputField
            size="SM"
            type="text"
            value={filter}
            placeholder={m.host_info_process_filter()}
            aria-label={m.host_info_process_filter()}
            onChange={event => setFilter(event.target.value)}
          />
        </div>
        <Button size="SM" theme="light" text={m.host_info_expand_all()} onClick={expandAll} />
        <Button
          size="SM"
          theme="light"
          text={m.host_info_collapse_all()}
          onClick={() => setExpanded(new Set())}
        />
        <Button
          size="SM"
          theme="light"
          text={m.host_info_snapshot()}
          LeadingIcon={LuRefreshCw}
          loading={loading}
          disabled={disabled}
          onClick={onSnapshot}
        />
      </div>
      <div>
        <ProcessHeader withUser sinceStart={(snapshot.window_s ?? 0) <= 0} />
        {rows.length === 0 && (
          <div className="py-2 text-sm text-slate-600 dark:text-slate-400">
            {m.host_info_no_matches()}
          </div>
        )}
        {rows.map(({ node, depth, expanded: open }) => (
          <ProcessLine
            key={node.proc.pid}
            name={node.proc.name}
            pid={node.proc.pid}
            user={node.proc.user ?? ""}
            cpu={processCpu(node.proc, snapshot)}
            trend={trendOf(history, node.proc)}
            mem={node.proc.mem}
            depth={depth}
            toggle={
              node.children.length > 0 ? (
                <button
                  type="button"
                  className="text-slate-600 dark:text-slate-400"
                  aria-label={open ? m.host_info_collapse() : m.host_info_expand()}
                  aria-expanded={open}
                  onClick={() => toggle(node.proc.pid)}
                >
                  {open ? <LuChevronDown size={14} /> : <LuChevronRight size={14} />}
                </button>
              ) : null
            }
          />
        ))}
        {truncated && (
          <div className="py-2 text-xs text-slate-500">{m.host_info_list_truncated()}</div>
        )}
      </div>
    </div>
  );
}

// "Taken 12s ago", updating every second.
function SnapshotAge({ receivedAt }: { receivedAt: number }) {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const timer = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(timer);
  }, []);
  return (
    <>{m.host_info_snapshot_age({ age: formatUptime(Math.max(0, (now - receivedAt) / 1000)) })}</>
  );
}

function useDocumentVisible() {
  const [visible, setVisible] = useState(() => document.visibilityState !== "hidden");
  useEffect(() => {
    const update = () => setVisible(document.visibilityState !== "hidden");
    document.addEventListener("visibilitychange", update);
    return () => document.removeEventListener("visibilitychange", update);
  }, []);
  return visible;
}

// This component is mounted only while the Processes tab of an open panel is
// showing, so its lifetime is the "visible" condition for the cadence timer.
function ProcessesTab({ canListAll }: { canListAll: boolean }) {
  const { request, ready } = useDeviceRpc();
  const snapshot = useHostInfoStore(s => s.snapshot);
  const receivedAt = useHostInfoStore(s => s.snapshotReceivedAt);
  const history = useHostInfoStore(s => s.processHistory);
  const applySnapshot = useHostInfoStore(s => s.applySnapshot);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [cadence, setCadence] = useState(loadProcessCadence);
  const visible = useDocumentVisible();
  const inflight = useRef<Promise<void> | null>(null);
  const mounted = useRef(true);

  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);

  // One request at a time: a click during a running request joins it. Never
  // rejects; on an error the last snapshot stays and the error is shown.
  const takeSnapshot = useCallback((): Promise<void> => {
    if (inflight.current) return inflight.current;
    const run = (async () => {
      if (mounted.current) setLoading(true);
      try {
        const result = await request<HostProcessesResult>("getHostProcesses");
        applySnapshot(
          { ...result, processes: Array.isArray(result?.processes) ? result.processes : [] },
          Date.now(),
        );
        if (mounted.current) setError(null);
      } catch (e) {
        if (mounted.current) setError(errorText(e));
      } finally {
        inflight.current = null;
        if (mounted.current) setLoading(false);
      }
    })();
    inflight.current = run;
    return run;
  }, [request, applySnapshot]);

  const takeRef = useRef(takeSnapshot);
  takeRef.current = takeSnapshot;

  const timerOn = canListAll && ready && visible && cadence > 0;
  useEffect(() => {
    if (!timerOn) return;
    // Do not fire right after a snapshot that is younger than the cadence.
    const last = useHostInfoStore.getState().snapshotReceivedAt;
    const age = last > 0 ? Date.now() - last : Infinity;
    return startSnapshotLoop({
      run: () => takeRef.current(),
      intervalMs: cadence,
      firstDelayMs: Math.max(0, cadence - age),
    });
  }, [timerOn, cadence]);

  if (!canListAll) return <Message>{m.host_info_section_unsupported()}</Message>;

  const sinceStart = !snapshot || (snapshot.window_s ?? 0) <= 0;

  return (
    <div className="space-y-4">
      <div className="space-y-2">
        <div className="flex flex-wrap items-center gap-x-2 gap-y-1.5">
          <span className="text-xs text-slate-600 dark:text-slate-400">
            {m.host_info_cadence()}
          </span>
          {PROCESS_CADENCES_MS.map(ms => (
            <Button
              key={ms}
              size="XS"
              theme={cadence === ms ? "primary" : "light"}
              text={
                ms === 0
                  ? m.host_info_cadence_off()
                  : m.host_info_cadence_seconds({ seconds: ms / 1000 })
              }
              onClick={() => {
                setCadence(ms);
                saveProcessCadence(ms);
              }}
            />
          ))}
        </div>
        {snapshot && (
          <div className="space-y-0.5 text-xs text-slate-600 dark:text-slate-400">
            <div>
              <SnapshotAge receivedAt={receivedAt} />
              {" · "}
              {m.host_info_process_count({ count: snapshot.processes?.length ?? 0 })}
            </div>
            <div>
              {sinceStart
                ? m.host_info_cpu_since_start()
                : m.host_info_cpu_window({ seconds: Number((snapshot.window_s ?? 0).toFixed(1)) })}
            </div>
          </div>
        )}
      </div>

      {error && <Message tone="error">{m.host_info_load_failed({ error })}</Message>}

      {!snapshot ? (
        <div className="space-y-3">
          <Message>{m.host_info_snapshot_empty()}</Message>
          <Button
            size="SM"
            theme="primary"
            text={m.host_info_snapshot()}
            LeadingIcon={LuRefreshCw}
            loading={loading}
            disabled={!ready}
            onClick={() => void takeSnapshot()}
          />
        </div>
      ) : (
        <>
          <Section title={m.host_info_top_processes()}>
            <TopProcesses snapshot={snapshot} history={history} />
          </Section>
          <Section title={m.host_info_all_processes()}>
            <ProcessTree
              snapshot={snapshot}
              history={history}
              loading={loading}
              disabled={!ready}
              onSnapshot={() => void takeSnapshot()}
            />
          </Section>
        </>
      )}
    </div>
  );
}

// ---------------------------------------------------------------------------
// USB

function usbSpeed(speed: string | undefined) {
  switch (speed) {
    case "low":
      return "1.5 Mbps";
    case "full":
      return "12 Mbps";
    case "high":
      return "480 Mbps";
    case "super":
      return "5 Gbps";
    case "super+":
      return "10 Gbps";
    default:
      return speed || "";
  }
}

function UsbTab() {
  const { request, ready } = useDeviceRpc();
  const [devices, setDevices] = useState<HostUsbDevice[] | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const result = await request<HostUsbResult>("getHostUsbDevices");
      setDevices(Array.isArray(result?.devices) ? result.devices : []);
    } catch (e) {
      setError(errorText(e));
    } finally {
      setLoading(false);
    }
  }, [request]);

  // Loaded on demand: when the tab opens and when the connection is ready.
  useEffect(() => {
    if (ready) void load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [ready]);

  return (
    <div className="space-y-3">
      <Button
        size="SM"
        theme="light"
        text={m.host_info_refresh()}
        LeadingIcon={LuRefreshCw}
        loading={loading}
        disabled={!ready}
        onClick={() => void load()}
      />
      {error && <Message tone="error">{m.host_info_load_failed({ error })}</Message>}
      {!devices && loading && <Message>{m.loading()}</Message>}
      {devices && devices.length === 0 && <Message>{m.host_info_no_usb()}</Message>}
      {devices?.map((device, index) => (
        <GridCard key={`${device.bus}-${device.address}-${index}`}>
          <div className="space-y-1 p-3">
            <div className="flex items-center justify-between gap-x-2 text-sm font-semibold text-black dark:text-white">
              <span className="truncate">
                {device.product || device.manufacturer || m.host_info_unknown()}
              </span>
              <span className="shrink-0 font-mono text-xs font-normal text-slate-600 dark:text-slate-400">
                {formatUsbId(device.vid, device.pid)}
              </span>
            </div>
            <Row label={m.host_info_manufacturer()} value={device.manufacturer} />
            <Row label={m.host_info_usb_speed()} value={usbSpeed(device.speed)} />
            <Row
              label={m.host_info_usb_bus()}
              value={device.bus ? `${device.bus}${device.address ? `:${device.address}` : ""}` : ""}
            />
          </div>
        </GridCard>
      ))}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Panel

export default function HostInfoSidebar() {
  const { setSidebarView } = useUiStore();
  const state = useHostInfoStore(s => s.state);
  const metrics = useHostInfoStore(s => s.metrics);
  const [tab, setTab] = useState<Tab>("overview");
  useHostInfoWatch();

  const sections = state?.sections ?? {};
  const tabs: Tab[] = ["overview", "storage", "network", "processes", "usb"];

  return (
    <div className="grid h-full grid-rows-(--grid-headerBody) shadow-xs">
      <SidebarHeader title={m.host_info_title()} setSidebarView={setSidebarView} />
      <div className="flex h-full min-h-0 flex-col bg-white dark:bg-slate-900">
        <div className="flex shrink-0 gap-x-1 border-b border-b-slate-800/20 px-4 py-1.5 dark:border-b-slate-300/20">
          {tabs.map(id => (
            <Button
              key={id}
              size="XS"
              theme={tab === id ? "primary" : "light"}
              text={tabLabel(id)}
              onClick={() => setTab(id)}
            />
          ))}
        </div>
        <div className="grow space-y-4 overflow-y-scroll px-4 py-4 pb-8">
          {state === null ? (
            <Message>{m.loading()}</Message>
          ) : !state.available ? (
            <Message>{m.host_info_unavailable()}</Message>
          ) : !tabSupported(tab, sections) ? (
            <Message>{m.host_info_section_unsupported()}</Message>
          ) : tab === "overview" ? (
            <OverviewTab inventory={state.inventory} metrics={metrics} sections={sections} />
          ) : tab === "storage" ? (
            <StorageTab inventory={state.inventory} metrics={metrics} />
          ) : tab === "network" ? (
            <NetworkTab inventory={state.inventory} metrics={metrics} />
          ) : tab === "processes" ? (
            <ProcessesTab canListAll={!!sections.processes} />
          ) : (
            <UsbTab />
          )}
        </div>
      </div>
    </div>
  );
}
