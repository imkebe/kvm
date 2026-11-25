import { useCallback, useEffect, useState } from "react";
import { CloseButton } from "@headlessui/react";
import { LuRefreshCw } from "react-icons/lu";
import validator from "validator";

import { cx } from "@/cva.config";
import { Button } from "@components/Button";
import { InputFieldWithLabel } from "@components/InputField";
import Modal from "@components/Modal";
import { SelectMenuBasic } from "@components/SelectMenuBasic";
import {
  AddressCheck,
  NeighborEntry,
  ScanMethod,
  ScanResult,
  useNetworkDiscovery,
} from "@hooks/useNetworkDiscovery";
import { m } from "@localizations/messages.js";

type Tab = "scan" | "neighbors" | "check";

function TabBar({ value, onChange }: { value: Tab; onChange: (tab: Tab) => void }) {
  const tabs: [Tab, string][] = [
    ["scan", m.network_discovery_tab_scan()],
    ["neighbors", m.network_discovery_tab_neighbors()],
    ["check", m.network_discovery_tab_check()],
  ];
  return (
    <div role="tablist" className="flex gap-1 rounded-md bg-slate-100 p-1 dark:bg-slate-800">
      {tabs.map(([tab, label]) => (
        <button
          key={tab}
          type="button"
          role="tab"
          aria-selected={value === tab}
          onClick={() => onChange(tab)}
          className={cx(
            "flex-1 rounded px-3 py-1.5 text-sm font-medium transition-colors",
            value === tab
              ? "bg-white text-slate-950 shadow-sm dark:bg-slate-700 dark:text-white"
              : "text-slate-600 hover:text-slate-900 dark:text-slate-400 dark:hover:text-white",
          )}
        >
          {label}
        </button>
      ))}
    </div>
  );
}

function Table({ head, rows }: { head: string[]; rows: (string | React.ReactNode)[][] }) {
  return (
    <div className="max-h-80 overflow-auto rounded border border-slate-800/10 dark:border-slate-300/20">
      <table className="w-full text-left text-xs">
        <thead className="sticky top-0 bg-slate-50 text-slate-600 dark:bg-slate-800 dark:text-slate-300">
          <tr>
            {head.map(title => (
              <th key={title} className="px-2 py-1.5 font-medium">
                {title}
              </th>
            ))}
          </tr>
        </thead>
        <tbody className="font-mono text-slate-900 dark:text-slate-100">
          {rows.map((row, index) => (
            <tr key={index} className="border-t border-slate-800/5 dark:border-slate-300/10">
              {row.map((cell, cellIndex) => (
                <td key={cellIndex} className="px-2 py-1 break-all">
                  {cell}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function ErrorText({ error }: { error: string | null }) {
  if (!error) return null;
  return <p className="text-xs text-red-600 dark:text-red-400">{error}</p>;
}

function ScanTab() {
  const { scan, scanDefaults } = useNetworkDiscovery();
  const [method, setMethod] = useState<ScanMethod>("arp");
  const [target, setTarget] = useState("");
  const [running, setRunning] = useState(false);
  const [result, setResult] = useState<ScanResult | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    scanDefaults().then(
      defaults => setTarget(current => current || defaults.ipv4 || ""),
      () => undefined,
    );
  }, [scanDefaults]);

  const help = {
    arp: m.network_discovery_method_arp_help(),
    ping: m.network_discovery_method_ping_help(),
    ipv6: m.network_discovery_method_ipv6_help(),
  }[method];

  const run = () => {
    setRunning(true);
    setError(null);
    scan(method, method === "ipv6" ? "" : target.trim()).then(
      found => {
        setResult(found);
        setRunning(false);
      },
      (failure: Error) => {
        setError(failure.message);
        setRunning(false);
      },
    );
  };

  return (
    <div className="space-y-3">
      <div className="grid gap-3 sm:grid-cols-[auto_1fr_auto] sm:items-end">
        <SelectMenuBasic
          size="SM"
          label={m.network_discovery_method()}
          value={method}
          options={[
            { value: "arp", label: m.network_discovery_method_arp() },
            { value: "ping", label: m.network_discovery_method_ping() },
            { value: "ipv6", label: m.network_discovery_method_ipv6() },
          ]}
          onChange={event => setMethod(event.target.value as ScanMethod)}
        />
        {method === "ipv6" ? (
          <div className="hidden sm:block" />
        ) : (
          <InputFieldWithLabel
            size="SM"
            label={m.network_discovery_subnet()}
            placeholder="192.168.1.0/24"
            value={target}
            onChange={event => setTarget(event.target.value)}
          />
        )}
        <Button
          size="SM"
          theme="primary"
          text={running ? m.network_discovery_scanning() : m.network_discovery_scan()}
          loading={running}
          disabled={running || (method !== "ipv6" && !target.trim())}
          onClick={run}
        />
      </div>
      <p className="text-xs text-slate-600 dark:text-slate-400">{help}</p>
      <ErrorText error={error} />
      {result && (
        <div className="space-y-1.5">
          <p className="text-xs text-slate-700 dark:text-slate-300">
            {m.network_discovery_summary({
              found: result.hosts.length,
              scanned: result.method === "ipv6" ? "ff02::1" : String(result.scanned),
              target: result.target,
              seconds: (result.duration_ms / 1000).toFixed(1),
            })}
          </p>
          <Table
            head={[
              m.network_discovery_col_address(),
              m.network_discovery_col_mac(),
              m.network_discovery_col_name(),
              m.network_discovery_col_rtt(),
            ]}
            rows={result.hosts.map(host => [
              <>
                {host.ip}
                {host.self && (
                  <span className="ml-1 font-sans text-[10px] text-blue-600 dark:text-blue-400">
                    {m.network_discovery_self()}
                  </span>
                )}
              </>,
              host.mac ?? "—",
              host.hostname ?? "—",
              host.rtt_ms !== undefined ? `${host.rtt_ms.toFixed(1)} ms` : "—",
            ])}
          />
        </div>
      )}
    </div>
  );
}

function NeighborsTab() {
  const { neighbors } = useNetworkDiscovery();
  const [entries, setEntries] = useState<NeighborEntry[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);

  const load = useCallback(() => {
    setLoading(true);
    setError(null);
    neighbors().then(
      list => {
        setEntries(list);
        setLoading(false);
      },
      (failure: Error) => {
        setError(failure.message);
        setLoading(false);
      },
    );
  }, [neighbors]);

  useEffect(() => load(), [load]);

  return (
    <div className="space-y-3">
      <div className="flex items-center justify-between gap-3">
        <p className="text-xs text-slate-600 dark:text-slate-400">
          {m.network_discovery_neighbors_help()}
        </p>
        <Button
          size="XS"
          theme="light"
          LeadingIcon={LuRefreshCw}
          text={m.network_discovery_refresh()}
          loading={loading}
          onClick={load}
        />
      </div>
      <ErrorText error={error} />
      {entries && (
        <Table
          head={[
            m.network_discovery_col_address(),
            m.network_discovery_col_mac(),
            m.network_discovery_col_state(),
          ]}
          rows={entries.map(entry => [
            <>
              {entry.ip}
              {entry.router && (
                <span className="ml-1 font-sans text-[10px] text-blue-600 dark:text-blue-400">
                  {m.network_discovery_router()}
                </span>
              )}
            </>,
            entry.mac ?? "—",
            entry.state,
          ])}
        />
      )}
    </div>
  );
}

function CheckTab() {
  const { checkAddress } = useNetworkDiscovery();
  const [address, setAddress] = useState("");
  const [result, setResult] = useState<AddressCheck | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [checking, setChecking] = useState(false);
  const valid = validator.isIP(address.trim(), 4);

  const run = () => {
    setChecking(true);
    setError(null);
    setResult(null);
    checkAddress(address.trim()).then(
      found => {
        setResult(found);
        setChecking(false);
      },
      (failure: Error) => {
        setError(failure.message);
        setChecking(false);
      },
    );
  };

  return (
    <div className="space-y-3">
      <div className="flex items-end gap-3">
        <div className="flex-1">
          <InputFieldWithLabel
            size="SM"
            label={m.network_ipv4_address()}
            placeholder="192.168.1.100"
            value={address}
            onChange={event => setAddress(event.target.value)}
          />
        </div>
        <Button
          size="SM"
          theme="primary"
          text={m.network_check_address()}
          loading={checking}
          disabled={!valid || checking}
          onClick={run}
        />
      </div>
      <p className="text-xs text-slate-600 dark:text-slate-400">
        {m.network_discovery_check_help()}
      </p>
      <ErrorText error={error} />
      {result && (
        <p
          className={cx(
            "text-sm font-medium",
            result.in_use ? "text-red-600 dark:text-red-400" : "text-green-700 dark:text-green-400",
          )}
        >
          {result.own
            ? m.network_check_address_own()
            : result.in_use
              ? m.network_address_in_use({ mac: result.mac ?? "" })
              : m.network_check_address_free()}
        </p>
      )}
    </div>
  );
}

// Network discovery from the KVM: hosts on a subnet, the neighbor table,
// and whether an address is taken.
export default function NetworkDiscoveryDialog({
  open,
  onClose,
}: {
  open: boolean;
  onClose: () => void;
}) {
  const [tab, setTab] = useState<Tab>("scan");
  return (
    <Modal open={open} onClose={onClose}>
      <div className="mx-auto max-w-3xl px-4">
        <div className="pointer-events-auto relative w-full overflow-hidden rounded-lg border border-slate-200 bg-white shadow-sm dark:border-slate-800 dark:bg-slate-900">
          <div className="max-h-[85vh] space-y-4 overflow-y-auto p-6 text-slate-900 dark:text-slate-100">
            <div className="space-y-1">
              <h2 className="font-semibold text-slate-950 dark:text-white">
                {m.network_discovery_title()}
              </h2>
              <p className="text-sm text-slate-700 dark:text-slate-300">
                {m.network_discovery_intro()}
              </p>
            </div>
            <TabBar value={tab} onChange={setTab} />
            {tab === "scan" && <ScanTab />}
            {tab === "neighbors" && <NeighborsTab />}
            {tab === "check" && <CheckTab />}
            <div className="flex justify-end">
              <CloseButton as={Button} size="SM" theme="light" text={m.close()} />
            </div>
          </div>
        </div>
      </div>
    </Modal>
  );
}
