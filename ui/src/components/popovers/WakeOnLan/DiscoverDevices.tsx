import { useCallback, useEffect, useState } from "react";
import { LuArrowLeft, LuPlus, LuRefreshCw } from "react-icons/lu";

import { Button } from "@components/Button";
import Card from "@components/Card";
import { InputFieldWithLabel } from "@components/InputField";
import { useNetworkDiscovery } from "@hooks/useNetworkDiscovery";
import { m } from "@localizations/messages.js";

interface Found {
  ip: string;
  mac: string;
  hostname?: string;
  // Answered the scan now; otherwise only in the KVM's neighbor table, as
  // a computer that is off but was seen recently.
  answering: boolean;
}

// The device name for a found host: its DNS name without the domain, else
// its address.
export function deviceNameFor(host: { ip: string; hostname?: string }) {
  return (host.hostname?.split(".")[0] || host.ip).slice(0, 30);
}

// Finds devices on the KVM's network for Wake on LAN: an ARP scan of the
// KVM's subnet, plus the IPv4 neighbor table for computers that do not
// answer now. Each one can be added with its MAC address filled in.
export default function DiscoverDevices({
  savedMacs,
  onPick,
  onBack,
}: {
  savedMacs: Set<string>;
  onPick: (name: string, macAddress: string) => void;
  onBack: () => void;
}) {
  const { scan, scanDefaults, neighbors } = useNetworkDiscovery();
  const [subnet, setSubnet] = useState("");
  const [found, setFound] = useState<Found[] | null>(null);
  const [running, setRunning] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const run = useCallback(
    async (target: string) => {
      if (!target) return;
      setRunning(true);
      setError(null);
      try {
        const [result, table] = await Promise.all([
          scan("arp", target),
          neighbors().catch(() => []),
        ]);
        const byMac = new Map<string, Found>();
        for (const host of result.hosts) {
          if (!host.mac || host.self) continue;
          byMac.set(host.mac, {
            ip: host.ip,
            mac: host.mac,
            hostname: host.hostname,
            answering: true,
          });
        }
        for (const entry of table) {
          if (entry.family !== "ipv4" || !entry.mac || byMac.has(entry.mac)) continue;
          if (entry.state === "failed" || entry.state === "incomplete") continue;
          byMac.set(entry.mac, { ip: entry.ip, mac: entry.mac, answering: false });
        }
        setFound([...byMac.values()]);
      } catch (failure) {
        setError(failure instanceof Error ? failure.message : String(failure));
      } finally {
        setRunning(false);
      }
    },
    [scan, neighbors],
  );

  useEffect(() => {
    scanDefaults().then(
      defaults => {
        const target = defaults.ipv4 ?? "";
        setSubnet(target);
        void run(target);
      },
      (failure: Error) => setError(failure.message),
    );
  }, [scanDefaults, run]);

  return (
    <div className="space-y-4">
      <div className="flex items-end gap-2">
        <div className="flex-1">
          <InputFieldWithLabel
            size="SM"
            label={m.network_discovery_subnet()}
            placeholder="192.168.1.0/24"
            value={subnet}
            onChange={event => setSubnet(event.target.value)}
            onKeyUp={event => event.stopPropagation()}
            onKeyDown={event => {
              event.stopPropagation();
              if (event.key === "Enter") void run(subnet.trim());
            }}
          />
        </div>
        <Button
          size="SM"
          theme="light"
          LeadingIcon={LuRefreshCw}
          text={running ? m.network_discovery_scanning() : m.network_discovery_scan()}
          loading={running}
          disabled={running || !subnet.trim()}
          onClick={() => void run(subnet.trim())}
        />
      </div>
      <p className="text-xs text-slate-600 dark:text-slate-400">{m.wake_on_lan_discover_help()}</p>
      {error && <p className="text-xs text-red-600 dark:text-red-400">{error}</p>}
      {found && (
        <Card className="max-h-64 overflow-y-auto">
          {found.length === 0 ? (
            <p className="p-3 text-sm text-slate-700 dark:text-slate-300">
              {m.wake_on_lan_discover_none()}
            </p>
          ) : (
            <div className="divide-y divide-slate-800/10 dark:divide-slate-300/20">
              {found.map(host => {
                const saved = savedMacs.has(host.mac.toLowerCase());
                return (
                  <div key={host.mac} className="flex items-center justify-between gap-2 px-3 py-2">
                    <div className="min-w-0">
                      <p className="truncate text-sm font-semibold text-slate-900 dark:text-slate-100">
                        {host.hostname ?? host.ip}
                      </p>
                      <p className="truncate font-mono text-xs text-slate-600 dark:text-slate-400">
                        {host.mac} · {host.ip}
                        {!host.answering && ` · ${m.wake_on_lan_discover_not_answering()}`}
                      </p>
                    </div>
                    {saved ? (
                      <span className="shrink-0 text-xs text-slate-500 dark:text-slate-400">
                        {m.wake_on_lan_discover_saved()}
                      </span>
                    ) : (
                      <Button
                        size="XS"
                        theme="light"
                        LeadingIcon={LuPlus}
                        text={m.wake_on_lan_discover_add()}
                        onClick={() => onPick(deviceNameFor(host), host.mac)}
                      />
                    )}
                  </div>
                );
              })}
            </div>
          )}
        </Card>
      )}
      <div className="flex justify-end">
        <Button
          size="SM"
          theme="light"
          text={m.back()}
          LeadingIcon={LuArrowLeft}
          onClick={onBack}
        />
      </div>
    </div>
  );
}
