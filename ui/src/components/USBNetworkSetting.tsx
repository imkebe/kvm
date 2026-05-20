import { useCallback, useEffect, useState } from "react";

import { Button } from "@components/Button";
import Fieldset from "@components/Fieldset";
import InputField from "@components/InputField";
import { SelectMenuBasic } from "@components/SelectMenuBasic";
import { SettingsItem } from "@components/SettingsItem";
import { SettingsSectionHeader } from "@components/SettingsSectionHeader";
import { JsonRpcResponse, useJsonRpc } from "@hooks/useJsonRpc";
import notifications from "@/notifications";

interface USBNetworkConfig {
  enabled: boolean;
  mode: "nat" | "routed";
  function: "rndis";
  interface_name: string;
  upstream_interface: string;
  address_cidr: string;
  dhcp_start: string;
  dhcp_end: string;
  dns: string;
}

interface USBNetworkState extends USBNetworkConfig {
  running: boolean;
  last_error?: string;
}

const defaultConfig: USBNetworkConfig = {
  enabled: false,
  mode: "nat",
  function: "rndis",
  interface_name: "usb0",
  upstream_interface: "eth0",
  address_cidr: "10.77.0.1/24",
  dhcp_start: "10.77.0.10",
  dhcp_end: "10.77.0.200",
  dns: "1.1.1.1",
};

export function USBNetworkSetting() {
  const { send } = useJsonRpc();
  const [config, setConfig] = useState<USBNetworkConfig>(defaultConfig);
  const [state, setState] = useState<USBNetworkState | null>(null);
  const [loading, setLoading] = useState(false);

  const syncState = useCallback(() => {
    send("getUsbNetworkState", {}, (resp: JsonRpcResponse) => {
      if ("error" in resp) return;
      const nextState = resp.result as USBNetworkState;
      setState(nextState);
      setConfig({
        enabled: nextState.enabled,
        mode: nextState.mode || defaultConfig.mode,
        function: "rndis",
        interface_name: nextState.interface_name || defaultConfig.interface_name,
        upstream_interface: nextState.upstream_interface || defaultConfig.upstream_interface,
        address_cidr: nextState.address_cidr || defaultConfig.address_cidr,
        dhcp_start: nextState.dhcp_start || defaultConfig.dhcp_start,
        dhcp_end: nextState.dhcp_end || defaultConfig.dhcp_end,
        dns: nextState.dns || defaultConfig.dns,
      });
    });
  }, [send]);

  useEffect(() => {
    syncState();
  }, [syncState]);

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

  const toggle = useCallback(async () => {
    setLoading(true);
    try {
      await saveConfig({ ...config, enabled: !state?.running });
      send(state?.running ? "stopUsbNetwork" : "startUsbNetwork", {}, (resp: JsonRpcResponse) => {
        setLoading(false);
        if ("error" in resp) {
          notifications.error(resp.error.data || resp.error.message);
          return;
        }
        setState(resp.result as USBNetworkState);
        notifications.success(state?.running ? "USB network stopped" : "USB network started");
      });
    } catch (error) {
      setLoading(false);
      notifications.error(error instanceof Error ? error.message : "Failed to update USB network");
    }
  }, [config, saveConfig, send, state?.running]);

  return (
    <Fieldset disabled={loading} className="space-y-4">
      <div className="h-px w-full bg-slate-800/10 dark:bg-slate-300/20" />
      <SettingsSectionHeader
        title="USB Network"
        description="Share network access with the target computer over USB Ethernet"
      />
      <SettingsItem
        title="Mode"
        description="NAT is self-contained; routed mode requires an upstream route"
      >
        <SelectMenuBasic
          size="SM"
          value={config.mode}
          onChange={e =>
            setConfig(prev => ({ ...prev, mode: e.target.value as USBNetworkConfig["mode"] }))
          }
          options={[
            { value: "nat", label: "NAT" },
            { value: "routed", label: "Routed" },
          ]}
        />
      </SettingsItem>
      <SettingsItem title="USB Interface" description="Network interface created by the USB gadget">
        <InputField
          size="SM"
          value={config.interface_name}
          onChange={e => setConfig(prev => ({ ...prev, interface_name: e.target.value }))}
        />
      </SettingsItem>
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
