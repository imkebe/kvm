import { useEffect } from "react";
import { create } from "zustand";

import { useRTCStore } from "@hooks/stores";
import { useJsonRpc, JsonRpcResponse } from "@hooks/useJsonRpc";
import { formatters } from "@/utils";

// Types of the host info contract (schema 1). Unknown fields are ignored;
// missing values arrive as empty strings, 0, empty arrays or null.

export interface HostInventory {
  schema?: number;
  os?: {
    family?: string;
    name?: string;
    version?: string;
    kernel?: string;
    arch?: string;
    hostname?: string;
  };
  boot_time?: number;
  cpu?: {
    brand?: string;
    vendor?: string;
    physical_cores?: number;
    logical_cores?: number;
    frequency_mhz?: number;
  };
  memory?: { total?: number; swap_total?: number };
  gpus?: { name?: string; vendor?: string; vram?: number; driver?: string }[];
  disks?: {
    name?: string;
    mount?: string;
    fs?: string;
    total?: number;
    available?: number;
    removable?: boolean;
  }[];
  networks?: { name?: string; mac?: string; addresses?: string[]; up?: boolean }[];
}

export interface HostMetrics {
  schema?: number;
  t?: number;
  uptime_s?: number;
  cpu?: { total?: number; per_core?: number[] };
  load?: number[];
  memory?: { used?: number; total?: number; swap_used?: number; swap_total?: number };
  networks?: {
    name?: string;
    rx_bps?: number;
    tx_bps?: number;
    rx_total?: number;
    tx_total?: number;
  }[];
  disks?: { mount?: string; total?: number; available?: number }[];
  temps?: { label?: string; c?: number }[];
  gpus?: { name?: string; load?: number | null; vram_used?: number | null }[];
  // Count from the most recent process snapshot; 0 until one was taken.
  process_count?: number;
}

export interface HostProcess {
  pid: number;
  ppid: number;
  name?: string;
  user?: string;
  // Average percent of one core over the snapshot window (see window_s).
  cpu?: number;
  // Accumulated CPU time since the process started; 0 when unknown.
  cpu_time_ms?: number;
  mem?: number;
  // Unix seconds; 0 when unknown.
  start?: number;
}

export interface HostProcessesResult {
  schema?: number;
  // Snapshot time, unix milliseconds.
  t?: number;
  // Seconds since the previous snapshot; 0 on the first one (every cpu is 0).
  window_s?: number;
  processes?: HostProcess[];
}

export interface HostUsbDevice {
  bus?: string;
  address?: number;
  vid?: number;
  pid?: number;
  manufacturer?: string;
  product?: string;
  class?: number;
  speed?: string;
}

export interface HostUsbResult {
  schema?: number;
  devices?: HostUsbDevice[];
}

export interface HostInfoSections {
  inventory?: boolean;
  metrics?: boolean;
  processes?: boolean;
  usb?: boolean;
}

// The getHostInfo reply; the hostInfoState event is the same without metrics.
export interface HostInfoState {
  available: boolean;
  sections?: HostInfoSections;
  inventory?: HostInventory | null;
  metrics?: HostMetrics | null;
}

// ---------------------------------------------------------------------------
// Formatting

export function formatPercent(value: number | null | undefined, decimals = 1): string {
  if (value == null || !Number.isFinite(value)) return "–";
  return `${value.toFixed(decimals)}%`;
}

export function formatBytes(bytes: number | null | undefined): string {
  if (bytes == null || !Number.isFinite(bytes) || bytes < 0) return "–";
  return formatters.bytes(bytes, 1);
}

export function formatRate(bytesPerSecond: number | null | undefined): string {
  if (bytesPerSecond == null || !Number.isFinite(bytesPerSecond) || bytesPerSecond < 0) return "–";
  return `${formatters.bytes(bytesPerSecond, 1)}/s`;
}

// 93784 -> "1d 2h 3m"; below a minute "45s".
export function formatUptime(seconds: number | null | undefined): string {
  if (seconds == null || !Number.isFinite(seconds) || seconds < 0) return "–";
  const total = Math.floor(seconds);
  const days = Math.floor(total / 86400);
  const hours = Math.floor((total % 86400) / 3600);
  const minutes = Math.floor((total % 3600) / 60);
  if (days > 0) return `${days}d ${hours}h ${minutes}m`;
  if (hours > 0) return `${hours}h ${minutes}m`;
  if (minutes > 0) return `${minutes}m ${total % 60}s`;
  return `${total}s`;
}

function hex4(n: number | undefined): string {
  return (Number.isFinite(n) ? (n as number) : 0).toString(16).padStart(4, "0");
}

export function formatUsbId(vid: number | undefined, pid: number | undefined): string {
  return `${hex4(vid)}:${hex4(pid)}`;
}

export function usedPercent(used: number | undefined, total: number | undefined): number {
  if (!used || !total || total <= 0) return 0;
  return Math.min(100, Math.max(0, (used / total) * 100));
}

// ---------------------------------------------------------------------------
// Process tree

export interface ProcessNode {
  proc: HostProcess;
  children: ProcessNode[];
}

// Siblings: cpu descending, then name, then pid.
export function makeProcessComparator(cpuOf: (proc: HostProcess) => number) {
  return (a: ProcessNode, b: ProcessNode) => {
    const name = (a.proc.name ?? "").toLowerCase();
    const other = (b.proc.name ?? "").toLowerCase();
    return (
      cpuOf(b.proc) - cpuOf(a.proc) ||
      (name < other ? -1 : name > other ? 1 : 0) ||
      a.proc.pid - b.proc.pid
    );
  };
}

// The cpu figure to show for a process of a snapshot: the averaged value, or
// on the first snapshot (window_s 0) the lifetime average
// cpu_time_ms / (snapshot time - start). null when it cannot be known.
export function processCpu(
  proc: HostProcess,
  snapshot: Pick<HostProcessesResult, "t" | "window_s">,
): number | null {
  if ((snapshot.window_s ?? 0) > 0) return proc.cpu ?? 0;
  const cpuTime = proc.cpu_time_ms ?? 0;
  const start = proc.start ?? 0;
  const now = snapshot.t ?? 0;
  if (cpuTime <= 0 || start <= 0 || now <= 0) return null;
  const elapsed = now - start * 1000;
  if (elapsed <= 0) return null;
  return (cpuTime / elapsed) * 100;
}

// Builds the forest by pid/ppid. A process whose parent is missing (orphan),
// is itself, or 0 becomes a root. Parent cycles are broken by promoting one
// member of the cycle to a root. Duplicate pids keep the first entry.
export function buildProcessTree(
  processes: readonly HostProcess[],
  cpuOf: (proc: HostProcess) => number = proc => proc.cpu ?? 0,
): ProcessNode[] {
  const compareProcesses = makeProcessComparator(cpuOf);
  const nodes = new Map<number, ProcessNode>();
  for (const proc of processes) {
    if (!nodes.has(proc.pid)) nodes.set(proc.pid, { proc, children: [] });
  }

  const parentOf = new Map<number, number>();
  const roots: ProcessNode[] = [];
  for (const node of nodes.values()) {
    const ppid = node.proc.ppid;
    const parent = ppid !== node.proc.pid ? nodes.get(ppid) : undefined;
    if (parent) {
      parent.children.push(node);
      parentOf.set(node.proc.pid, parent.proc.pid);
    } else {
      roots.push(node);
    }
  }

  // Everything not reachable from a root sits on a cycle (or hangs off one).
  const visited = new Set<number>();
  const visit = (start: ProcessNode) => {
    const stack = [start];
    while (stack.length > 0) {
      const node = stack.pop() as ProcessNode;
      if (visited.has(node.proc.pid)) continue;
      visited.add(node.proc.pid);
      for (const child of node.children) stack.push(child);
    }
  };
  roots.forEach(visit);
  for (const node of nodes.values()) {
    if (visited.has(node.proc.pid)) continue;
    const parent = nodes.get(parentOf.get(node.proc.pid) as number);
    if (parent) parent.children = parent.children.filter(child => child !== node);
    parentOf.delete(node.proc.pid);
    roots.push(node);
    visit(node);
  }

  const sortDeep = (list: ProcessNode[]) => {
    const stack = [list];
    while (stack.length > 0) {
      const current = stack.pop() as ProcessNode[];
      current.sort(compareProcesses);
      for (const node of current) if (node.children.length > 1) stack.push(node.children);
    }
  };
  sortDeep(roots);
  return roots;
}

export interface ProcessRow {
  node: ProcessNode;
  depth: number;
  expanded: boolean;
}

// Visible rows of the tree. With a filter, only matching processes and their
// ancestors show, all expanded. Without one, only expanded nodes open.
export function flattenProcessTree(
  roots: readonly ProcessNode[],
  expanded: ReadonlySet<number>,
  filter: string,
  limit = 1000,
): { rows: ProcessRow[]; truncated: boolean } {
  const needle = filter.trim().toLowerCase();
  const rows: ProcessRow[] = [];
  let truncated = false;

  // Pids that match or have a matching descendant.
  let keep: Set<number> | null = null;
  if (needle) {
    keep = new Set<number>();
    const walk = (node: ProcessNode): boolean => {
      let hit =
        (node.proc.name ?? "").toLowerCase().includes(needle) ||
        String(node.proc.pid) === needle ||
        (node.proc.user ?? "").toLowerCase().includes(needle);
      for (const child of node.children) if (walk(child)) hit = true;
      if (hit) keep?.add(node.proc.pid);
      return hit;
    };
    roots.forEach(walk);
  }

  const stack: { node: ProcessNode; depth: number }[] = [];
  for (let i = roots.length - 1; i >= 0; i--) stack.push({ node: roots[i], depth: 0 });
  while (stack.length > 0) {
    const { node, depth } = stack.pop() as { node: ProcessNode; depth: number };
    if (keep && !keep.has(node.proc.pid)) continue;
    if (rows.length >= limit) {
      truncated = true;
      break;
    }
    const open = node.children.length > 0 && (keep !== null || expanded.has(node.proc.pid));
    rows.push({ node, depth, expanded: open });
    if (open) {
      for (let i = node.children.length - 1; i >= 0; i--) {
        stack.push({ node: node.children[i], depth: depth + 1 });
      }
    }
  }
  return { rows, truncated };
}

// ---------------------------------------------------------------------------
// Store

export const HOST_HISTORY_LENGTH = 60;

export interface HostGpuSample {
  // Percent busy and video memory in use (bytes); null when not reported.
  load: number | null;
  memory: number | null;
}

export interface HostHistorySample {
  // Seconds, as the chart axis wants it.
  date: number;
  cpu: number;
  memory: number;
  rx: number;
  tx: number;
  // One per metrics.gpus entry, in its order (the inventory's order).
  gpus: HostGpuSample[];
}

// Per-process trend of the averaged cpu, last PROCESS_TREND_LENGTH snapshots.
export const PROCESS_TREND_LENGTH = 12;

export interface ProcessSeries {
  // Snapshot times (unix ms) and the averaged cpu values, same length.
  t: number[];
  cpu: number[];
}

// Key = pid + start, so a reused pid starts a fresh series.
export type ProcessHistory = Record<string, ProcessSeries>;

export function processKey(proc: Pick<HostProcess, "pid" | "start">): string {
  return `${proc.pid}:${proc.start ?? 0}`;
}

// Returns the history after a snapshot: series of processes missing from it
// are dropped; averaged values are appended only when the snapshot has a
// window (the first one has none) and is newer than the series' last point.
export function advanceProcessHistory(
  prev: ProcessHistory,
  snapshot: HostProcessesResult,
  t: number,
): ProcessHistory {
  const next: ProcessHistory = {};
  const averaged = (snapshot.window_s ?? 0) > 0;
  for (const proc of snapshot.processes ?? []) {
    const key = processKey(proc);
    if (key in next) continue;
    const old = prev[key];
    if (!averaged || (old && old.t.length > 0 && old.t[old.t.length - 1] >= t)) {
      next[key] = old ?? { t: [], cpu: [] };
      continue;
    }
    next[key] = {
      t: [...(old?.t ?? []), t].slice(-PROCESS_TREND_LENGTH),
      cpu: [...(old?.cpu ?? []), proc.cpu ?? 0].slice(-PROCESS_TREND_LENGTH),
    };
  }
  return next;
}

// Calls run() repeatedly, the next call only after the previous one settled
// (so a slow answer slows the cadence and calls never overlap). Errors are
// ignored: the loop keeps going. Returns a stop function.
export function startSnapshotLoop(options: {
  run: () => Promise<unknown>;
  intervalMs: number;
  firstDelayMs?: number;
  setTimer?: (fn: () => void, ms: number) => unknown;
  clearTimer?: (handle: unknown) => void;
}): () => void {
  const setTimer = options.setTimer ?? ((fn, ms) => setTimeout(fn, ms));
  const clearTimer = options.clearTimer ?? (handle => clearTimeout(handle as number));
  let stopped = false;
  let handle: unknown;
  const tick = async () => {
    if (stopped) return;
    try {
      await options.run();
    } catch {
      // The caller shows errors; keep trying at the cadence.
    }
    if (!stopped) handle = setTimer(() => void tick(), options.intervalMs);
  };
  handle = setTimer(() => void tick(), options.firstDelayMs ?? 0);
  return () => {
    stopped = true;
    clearTimer(handle);
  };
}

export const PROCESS_CADENCES_MS = [0, 2000, 5000, 10000] as const;
const CADENCE_STORAGE_KEY = "hostInfoProcessCadenceMs";

export function loadProcessCadence(): number {
  try {
    const value = Number(window.localStorage.getItem(CADENCE_STORAGE_KEY));
    return (PROCESS_CADENCES_MS as readonly number[]).includes(value) ? value : 0;
  } catch {
    return 0;
  }
}

export function saveProcessCadence(ms: number) {
  try {
    window.localStorage.setItem(CADENCE_STORAGE_KEY, String(ms));
  } catch {
    // Storage may be unavailable; the choice then lasts for this page only.
  }
}

interface HostInfoStore {
  state: HostInfoState | null;
  metrics: HostMetrics | null;
  history: HostHistorySample[];
  snapshot: HostProcessesResult | null;
  // Browser clock at receipt; the age shown is measured against this.
  snapshotReceivedAt: number;
  processHistory: ProcessHistory;
  applySnapshot: (snapshot: HostProcessesResult, receivedAt: number) => void;
  applyInfo: (info: HostInfoState) => void;
  applyState: (info: HostInfoState) => void;
  applyMetrics: (metrics: HostMetrics) => void;
  reset: () => void;
}

function stateOnly(info: HostInfoState): HostInfoState {
  return {
    available: !!info.available,
    sections: info.sections ?? {},
    inventory: info.inventory ?? null,
  };
}

export function sampleFromMetrics(metrics: HostMetrics): HostHistorySample {
  const networks = metrics.networks ?? [];
  return {
    date: metrics.t ? Math.floor(metrics.t / 1000) : Math.floor(Date.now() / 1000),
    cpu: metrics.cpu?.total ?? 0,
    memory: usedPercent(metrics.memory?.used, metrics.memory?.total),
    rx: networks.reduce((sum, n) => sum + (n.rx_bps ?? 0), 0),
    tx: networks.reduce((sum, n) => sum + (n.tx_bps ?? 0), 0),
    gpus: (metrics.gpus ?? []).map(gpu => ({
      load: finiteOrNull(gpu.load),
      memory: finiteOrNull(gpu.vram_used),
    })),
  };
}

function finiteOrNull(value: number | null | undefined): number | null {
  return value != null && Number.isFinite(value) ? value : null;
}

// Index of an inventory GPU in metrics.gpus: by name, else the same position.
export function gpuMetricsIndex(
  metrics: HostMetrics | null | undefined,
  name: string | undefined,
  index: number,
): number {
  const byName = name ? (metrics?.gpus ?? []).findIndex(gpu => gpu.name === name) : -1;
  return byName >= 0 ? byName : index;
}

const EMPTY_PROCESSES = { snapshot: null, snapshotReceivedAt: 0, processHistory: {} };

export const useHostInfoStore = create<HostInfoStore>(set => ({
  state: null,
  metrics: null,
  history: [],
  ...EMPTY_PROCESSES,

  applySnapshot: (snapshot, receivedAt) =>
    set(prev => ({
      snapshot,
      snapshotReceivedAt: receivedAt,
      processHistory: advanceProcessHistory(
        prev.processHistory,
        snapshot,
        snapshot.t || receivedAt,
      ),
    })),

  // getHostInfo reply: state plus the last metrics, if any.
  applyInfo: info =>
    set(prev => {
      const next = stateOnly(info);
      if (!next.available) return { state: next, ...EMPTY_PROCESSES, metrics: null, history: [] };
      return { state: next, metrics: info.metrics ?? prev.metrics };
    }),

  // hostInfoState event: availability or inventory changed.
  applyState: info =>
    set(prev => {
      const next = stateOnly(info);
      if (!next.available) return { state: next, ...EMPTY_PROCESSES, metrics: null, history: [] };
      return { state: next, metrics: prev.metrics };
    }),

  applyMetrics: metrics =>
    set(prev => {
      const sample = sampleFromMetrics(metrics);
      const last = prev.history[prev.history.length - 1];
      // Samples arrive about once a second; keep one per second at most.
      const history =
        last && last.date >= sample.date
          ? [...prev.history.slice(0, -1), sample]
          : [...prev.history, sample];
      return { metrics, history: history.slice(-HOST_HISTORY_LENGTH) };
    }),

  reset: () => set({ state: null, metrics: null, history: [], ...EMPTY_PROCESSES }),
}));

// Chart points for the existing Metric component: a fixed window of 120
// seconds, empty where no sample arrived or pick gives null.
export function historyChartData(
  history: readonly HostHistorySample[],
  pick: (sample: HostHistorySample) => number | null | undefined,
  now = Math.floor(Date.now() / 1000),
  window = 120,
): { date: number; metric: number | null }[] {
  const bySecond = new Map<number, number | null>();
  for (const sample of history) bySecond.set(sample.date, pick(sample) ?? null);
  const points: { date: number; metric: number | null }[] = [];
  for (let t = now - window + 1; t <= now; t++)
    points.push({ date: t, metric: bySecond.get(t) ?? null });
  return points;
}

// ---------------------------------------------------------------------------
// Hooks

// Called from the device route: keeps availability current so the action bar
// can show its button without the panel open.
export function useHostInfoAvailability() {
  const { send } = useJsonRpc();
  const channel = useRTCStore(state => state.rpcDataChannel);
  const ready = channel?.readyState === "open";
  const applyInfo = useHostInfoStore(state => state.applyInfo);
  const reset = useHostInfoStore(state => state.reset);

  useEffect(() => {
    if (!ready) {
      reset();
      return;
    }
    void send("getHostInfo", {}, (resp: JsonRpcResponse) => {
      if ("error" in resp) return;
      applyInfo(resp.result as HostInfoState);
    });
  }, [ready, send, applyInfo, reset]);
}

// While the panel is mounted: one getHostInfo, then watchHostInfo every 5 s
// (the KVM lease is 12 s). Stops on unmount.
export function useHostInfoWatch() {
  const { send } = useJsonRpc();
  const channel = useRTCStore(state => state.rpcDataChannel);
  const ready = channel?.readyState === "open";
  const applyInfo = useHostInfoStore(state => state.applyInfo);

  useEffect(() => {
    if (!ready) return;
    let stopped = false;
    void send("getHostInfo", {}, (resp: JsonRpcResponse) => {
      if (stopped || "error" in resp) return;
      applyInfo(resp.result as HostInfoState);
    });
    const watch = () => void send("watchHostInfo", {});
    watch();
    const timer = window.setInterval(watch, 5000);
    return () => {
      stopped = true;
      window.clearInterval(timer);
    };
  }, [ready, send, applyInfo]);
}
