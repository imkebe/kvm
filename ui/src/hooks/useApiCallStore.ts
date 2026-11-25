import { create } from "zustand";

// What programs do with the KVM's APIs (api_call_log.go): MCP tool calls,
// device API calls and terminal API shells. The KVM sends each call as an
// "apiCall" event; getApiCallLog gives the recent ones and the counts since
// the app started.

export type ApiCallSource = "mcp" | "api" | "terminal";

export interface ApiCallEntry {
  id: number;
  at: number; // Unix ms
  source: ApiCallSource;
  name: string;
  duration_ms: number;
  ok: boolean;
  error?: string;
  args?: string;
  token?: string;
  remote?: string;
  via_mcp?: boolean;
}

export interface ApiCallCount {
  source: ApiCallSource;
  name: string;
  calls: number;
  errors: number;
  total_ms: number;
  last_at: number;
}

export interface ApiCallLog {
  started: number;
  // Older firmware sent null for an empty record.
  entries: ApiCallEntry[] | null;
  counts: ApiCallCount[] | null;
  totals: Partial<Record<ApiCallSource, number>> | null;
}

const KEEP = 300;

interface ApiCallState {
  entries: ApiCallEntry[]; // oldest first
  counts: Record<string, ApiCallCount>; // by source and name
  totals: Partial<Record<ApiCallSource, number>>;
  started: number;
  // When the last call arrived here (Date.now()), and how many arrived: a
  // change of pulse lights the MCP card.
  lastCallAt: number;
  pulse: number;
  push: (entry: ApiCallEntry) => void;
  load: (log: ApiCallLog) => void;
  // After a reset: the KVM's empty record replaces everything here.
  replace: (log: ApiCallLog) => void;
}

const countKey = (source: string, name: string) => `${source}\u0000${name}`;

export const useApiCallStore = create<ApiCallState>((set, get) => ({
  entries: [],
  counts: {},
  totals: {},
  started: 0,
  lastCallAt: 0,
  pulse: 0,

  push: entry => {
    const state = get();
    if (state.entries.some(known => known.id === entry.id)) return;
    const key = countKey(entry.source, entry.name);
    const previous = state.counts[key] ?? {
      source: entry.source,
      name: entry.name,
      calls: 0,
      errors: 0,
      total_ms: 0,
      last_at: 0,
    };
    set({
      entries: [...state.entries.slice(-(KEEP - 1)), entry],
      counts: {
        ...state.counts,
        [key]: {
          ...previous,
          calls: previous.calls + 1,
          errors: previous.errors + (entry.ok ? 0 : 1),
          total_ms: previous.total_ms + entry.duration_ms,
          last_at: entry.at,
        },
      },
      totals: { ...state.totals, [entry.source]: (state.totals[entry.source] ?? 0) + 1 },
      lastCallAt: Date.now(),
      pulse: state.pulse + 1,
    });
  },

  // The KVM's record replaces what is here; calls that arrived as events
  // after it was read stay in the list (the counts are the KVM's).
  load: log => {
    const entries = log.entries ?? [];
    const newest = entries.at(-1)?.id ?? 0;
    const later = get().entries.filter(entry => entry.id > newest);
    const counts: Record<string, ApiCallCount> = {};
    for (const count of log.counts ?? []) counts[countKey(count.source, count.name)] = count;
    set({
      entries: [...entries, ...later].slice(-KEEP),
      counts,
      totals: log.totals ?? {},
      started: log.started,
    });
  },

  replace: log => {
    const counts: Record<string, ApiCallCount> = {};
    for (const count of log.counts ?? []) counts[countKey(count.source, count.name)] = count;
    set({ entries: log.entries ?? [], counts, totals: log.totals ?? {}, started: log.started });
  },
}));

export function apiCallCounts(counts: Record<string, ApiCallCount>) {
  return Object.values(counts).sort((a, b) => b.calls - a.calls || a.name.localeCompare(b.name));
}
