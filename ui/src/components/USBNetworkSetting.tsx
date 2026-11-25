import { useCallback, useEffect, useState } from "react";

import { Button } from "@components/Button";
import Fieldset from "@components/Fieldset";
import InputField from "@components/InputField";
import { SelectMenuBasic } from "@components/SelectMenuBasic";
import { SettingsItem } from "@components/SettingsItem";
import { NetbirdTargetPanel } from "@components/NetbirdTargetPanel";
import { SettingsSectionHeader } from "@components/SettingsSectionHeader";
import { useCapability } from "@hooks/stores";
import { JsonRpcResponse, useJsonRpc } from "@hooks/useJsonRpc";
import { m } from "@localizations/messages.js";
import notifications from "@/notifications";

interface USBNetworkConfig {
  enabled: boolean;
  // Exclusive: in netbird or tailscale the target reaches only that VPN.
  mode: "nat" | "routed" | "netbird" | "tailscale";
  function: "ncm" | "ecm" | "rndis";
  interface_name: string;
  upstream_interface: string;
  address_cidr: string;
  dhcp_start: string;
  dhcp_end: string;
  dns: string;
  adapter: string;
  host_mac: string;
  device_mac: string;
}

interface UsbNetworkAdapter {
  id: string;
  oui: string;
  host_mac: string;
  device_mac: string;
}

// Names of the adapter presets (their MAC vendor prefixes); the KVM lists
// the presets with the addresses each gives this KVM.
const ADAPTER_LABELS: Record<string, string> = {
  jetkvm: "JetKVM (default, locally administered)",
  proxmox: "Proxmox VE",
  qemu: "QEMU / KVM (virtio, e1000)",
  vmware: "VMware (vmxnet3, e1000)",
  hyperv: "Microsoft Hyper-V",
  virtualbox: "Oracle VirtualBox",
  xen: "Xen",
  parallels: "Parallels",
  realtek: "Realtek",
  intel: "Intel",
  asix: "ASIX",
};

interface USBNetworkState extends USBNetworkConfig {
  running: boolean;
  last_error?: string;
  netbird_up?: boolean;
  tailscale_up?: boolean;
  shared_routes?: string[];
}

const defaultConfig: USBNetworkConfig = {
  enabled: false,
  mode: "nat",
  function: "ncm",
  interface_name: "usb0",
  upstream_interface: "eth0",
  address_cidr: "10.77.0.1/24",
  dhcp_start: "10.77.0.10",
  dhcp_end: "10.77.0.200",
  dns: "1.1.1.1",
  adapter: "jetkvm",
  host_mac: "",
  device_mac: "",
};

export function USBNetworkSetting() {
  const { send } = useJsonRpc();
  const [config, setConfig] = useState<USBNetworkConfig>(defaultConfig);
  const [state, setState] = useState<USBNetworkState | null>(null);
  const [loading, setLoading] = useState(false);
  const hasNetbird = useCapability("netbird");
  const hasTailscale = useCapability("tailscale");

  const syncState = useCallback(() => {
    send("getUsbNetworkState", {}, (resp: JsonRpcResponse) => {
      if ("error" in resp) return;
      const nextState = resp.result as USBNetworkState;
      setState(nextState);
      setConfig({
        enabled: nextState.enabled,
        mode: nextState.mode || defaultConfig.mode,
        function: nextState.function || defaultConfig.function,
        interface_name: nextState.interface_name || defaultConfig.interface_name,
        upstream_interface: nextState.upstream_interface || defaultConfig.upstream_interface,
        address_cidr: nextState.address_cidr || defaultConfig.address_cidr,
        dhcp_start: nextState.dhcp_start || defaultConfig.dhcp_start,
        dhcp_end: nextState.dhcp_end || defaultConfig.dhcp_end,
        dns: nextState.dns || defaultConfig.dns,
        adapter: nextState.adapter || defaultConfig.adapter,
        host_mac: nextState.host_mac || "",
        device_mac: nextState.device_mac || "",
      });
    });
  }, [send]);

  useEffect(() => {
    syncState();
  }, [syncState]);

  const [adapters, setAdapters] = useState<UsbNetworkAdapter[]>([]);
  useEffect(() => {
    send("getUsbNetworkAdapters", {}, (resp: JsonRpcResponse) => {
      if ("error" in resp) return;
      setAdapters(resp.result as UsbNetworkAdapter[]);
    });
  }, [send]);

  const chooseAdapter = (id: string) => {
    const preset = adapters.find(adapter => adapter.id === id);
    setConfig(prev => ({
      ...prev,
      adapter: id,
      // A custom adapter starts from the addresses shown now.
      host_mac: preset?.host_mac ?? prev.host_mac,
      device_mac: preset?.device_mac ?? prev.device_mac,
    }));
  };

  const saveIdentity = async () => {
    setLoading(true);
    try {
      await saveConfig(config);
      notifications.success("USB network adapter saved; the target reconnects the adapter");
    } catch (error) {
      notifications.error(error instanceof Error ? error.message : "Failed to save the adapter");
    } finally {
      setLoading(false);
    }
  };

  const identityChanged =
    !!state &&
    (config.adapter !== (state.adapter || "jetkvm") ||
      (config.adapter === "custom" &&
        (config.host_mac !== state.host_mac || config.device_mac !== state.device_mac)));

  const saveConfig = useCallback(
    (nextConfig: USBNetworkConfig) =>
      new Promise<void>((resolve, reject) => {
        send("setUsbNetworkConfig", { config: nextConfig }, (resp: JsonRpcResponse) => {
          if ("error" in resp) {
            reject(new Error(resp.error.data || resp.error.message));
            return;
          }
          setState(resp.result as USBNetworkState);
          resolve();
        });
      }),
    [send],
  );

  // The mode applies at once (the rest of the form on Start).
  const setMode = (mode: USBNetworkConfig["mode"]) => {
    setLoading(true);
    send("setUsbNetworkMode", { mode }, (resp: JsonRpcResponse) => {
      setLoading(false);
      if ("error" in resp) {
        notifications.error(resp.error.data || resp.error.message);
        syncState();
        return;
      }
      setState(resp.result as USBNetworkState);
      setConfig(prev => ({ ...prev, mode }));
    });
  };
  const vpnMode = config.mode === "netbird" || config.mode === "tailscale";

  const toggle = useCallback(async () => {
    setLoading(true);
    try {
      await saveConfig({ ...config, enabled: !state?.running });
      send(state?.running ? "stopUsbNetwork" : "startUsbNetwork", {}, (resp: JsonRpcResponse) => {
        setLoading(false);
        if ("error" in resp) {
          notifications.error(resp.error.data || resp.error.message);
          syncState();
          return;
        }
        setState(resp.result as USBNetworkState);
        notifications.success(state?.running ? "USB network stopped" : "USB network started");
      });
    } catch (error) {
      setLoading(false);
      notifications.error(error instanceof Error ? error.message : "Failed to update USB network");
    }
  }, [config, saveConfig, send, state?.running, syncState]);

  return (
    <Fieldset disabled={loading} className="space-y-4">
      <div className="h-px w-full bg-slate-800/10 dark:bg-slate-300/20" />
      <SettingsSectionHeader
        title="USB Network"
        description="Share network access with the target computer over USB Ethernet"
      />
      <SettingsItem
        title="USB Ethernet Protocol"
        description="NCM and ECM use the built-in macOS and Linux Ethernet drivers"
      >
        <SelectMenuBasic
          size="SM"
          value={config.function}
          onChange={e =>
            setConfig(prev => ({
              ...prev,
              function: e.target.value as USBNetworkConfig["function"],
            }))
          }
          options={[
            { value: "ncm", label: "CDC-NCM (macOS / Linux)" },
            { value: "ecm", label: "CDC-ECM (macOS / Linux)" },
            { value: "rndis", label: "RNDIS (Windows)" },
          ]}
        />
      </SettingsItem>
      <SettingsItem
        title="Network adapter"
        description="How the adapter looks to the target and its network: the MAC vendor prefix a hypervisor gives its virtual cards. The name the target shows is the USB device name (USB settings, Identity)."
      >
        <SelectMenuBasic
          size="SM"
          value={config.adapter}
          onChange={e => chooseAdapter(e.target.value)}
          options={[
            ...(adapters.length
              ? adapters
              : [{ id: "jetkvm", oui: "02:1d:6b", host_mac: "", device_mac: "" }]
            ).map(adapter => ({
              value: adapter.id,
              label: `${ADAPTER_LABELS[adapter.id] ?? adapter.id} (${adapter.oui})`,
            })),
            { value: "custom", label: "Custom MAC addresses" },
          ]}
        />
      </SettingsItem>
      <div className="ml-2 space-y-3 border-l-2 border-slate-200 pl-4 dark:border-slate-700">
        <SettingsItem title="Target's MAC address" description="The adapter on the target computer">
          {config.adapter === "custom" ? (
            <InputField
              size="SM"
              value={config.host_mac}
              placeholder="bc:24:11:12:34:56"
              onChange={e => setConfig(prev => ({ ...prev, host_mac: e.target.value }))}
            />
          ) : (
            <span className="font-mono text-sm text-slate-700 dark:text-slate-300">
              {(adapters.find(adapter => adapter.id === config.adapter)?.host_mac ??
                config.host_mac) ||
                "—"}
            </span>
          )}
        </SettingsItem>
        <SettingsItem
          title="KVM's MAC address"
          description="The KVM's end of the link, the target's gateway"
        >
          {config.adapter === "custom" ? (
            <InputField
              size="SM"
              value={config.device_mac}
              placeholder="bc:24:11:12:34:57"
              onChange={e => setConfig(prev => ({ ...prev, device_mac: e.target.value }))}
            />
          ) : (
            <span className="font-mono text-sm text-slate-700 dark:text-slate-300">
              {(adapters.find(adapter => adapter.id === config.adapter)?.device_mac ??
                config.device_mac) ||
                "—"}
            </span>
          )}
        </SettingsItem>
        {identityChanged && (
          <div className="flex items-center justify-end gap-x-3">
            <p className="text-xs text-slate-600 dark:text-slate-400">
              Saving reconnects the USB adapter on the target.
            </p>
            <Button size="SM" theme="primary" text="Save adapter" onClick={saveIdentity} />
          </div>
        )}
      </div>
      <SettingsItem title="Mode" description={m.usb_network_mode_description()}>
        <SelectMenuBasic
          size="SM"
          value={config.mode}
          onChange={e => setMode(e.target.value as USBNetworkConfig["mode"])}
          options={[
            { value: "nat", label: "NAT" },
            { value: "routed", label: "Routed" },
            ...(hasNetbird || config.mode === "netbird"
              ? [{ value: "netbird", label: "NetBird" }]
              : []),
            ...(hasTailscale || config.mode === "tailscale"
              ? [{ value: "tailscale", label: "Tailscale" }]
              : []),
          ]}
        />
      </SettingsItem>
      {config.mode === "netbird" && <NetbirdTargetPanel usbRunning={!!state?.running} />}
      {config.mode === "tailscale" && state && (
        <VPNModeStatus
          up={!!state.tailscale_up}
          running={state.running}
          routes={state.shared_routes}
          waiting={m.usb_network_share_waiting_tailscale()}
          reachHint={m.usb_network_tailscale_reach_hint({ subnet: usbSubnet(config.address_cidr) })}
        />
      )}
      <SettingsItem title="USB Interface" description="Network interface created by the USB gadget">
        <InputField
          size="SM"
          value={config.interface_name}
          onChange={e => setConfig(prev => ({ ...prev, interface_name: e.target.value }))}
        />
      </SettingsItem>
      {!vpnMode && (
        <SettingsItem
          title="Upstream Interface"
          description="Network interface used for NAT or routing"
        >
          <InputField
            size="SM"
            value={config.upstream_interface}
            onChange={e => setConfig(prev => ({ ...prev, upstream_interface: e.target.value }))}
          />
        </SettingsItem>
      )}
      <SettingsItem title="USB Address" description="JetKVM address on the USB network">
        <InputField
          size="SM"
          value={config.address_cidr}
          onChange={e => setConfig(prev => ({ ...prev, address_cidr: e.target.value }))}
        />
      </SettingsItem>
      <div className="flex gap-x-2">
        <Button
          size="SM"
          theme="primary"
          loading={loading}
          text={state?.running ? "Stop USB Network" : "Start USB Network"}
          onClick={toggle}
        />
        <Button size="SM" theme="light" text="Refresh State" onClick={syncState} />
      </div>
      {state?.last_error && (
        <p className="text-xs text-red-700 dark:text-red-300">{state.last_error}</p>
      )}
    </Fieldset>
  );
}

// The USB subnet of an address such as 10.77.0.1/24 (IPv4 only).
function usbSubnet(addressCIDR: string) {
  const [address, bits] = addressCIDR.split("/");
  const prefix = Number(bits);
  const octets = address.split(".").map(Number);
  if (octets.length !== 4 || !Number.isInteger(prefix) || prefix < 0 || prefix > 32) {
    return addressCIDR;
  }
  const value = octets.reduce((acc, octet) => acc * 256 + octet, 0);
  const size = 2 ** (32 - prefix);
  const network = Math.floor(value / size) * size;
  const parts = [24, 16, 8, 0].map(shift => Math.floor(network / 2 ** shift) % 256);
  return `${parts.join(".")}/${prefix}`;
}

function VPNModeStatus({
  up,
  running,
  routes,
  waiting,
  reachHint,
}: {
  up: boolean;
  running: boolean;
  routes?: string[];
  waiting: string;
  reachHint: string;
}) {
  let status = waiting;
  if (!running) status = m.usb_network_share_when_running();
  else if (up) status = m.usb_network_share_active({ routes: (routes ?? []).join(", ") || "—" });
  return (
    <div className="ml-2 space-y-1 border-l-2 border-slate-200 pl-4 dark:border-slate-700">
      <p className="text-xs text-slate-600 dark:text-slate-400">{status}</p>
      {up && running && <p className="text-xs text-slate-500 dark:text-slate-400">{reachHint}</p>}
    </div>
  );
}
