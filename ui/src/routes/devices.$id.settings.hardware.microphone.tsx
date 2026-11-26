import { useCallback, useEffect, useState } from "react";
import { LuTriangleAlert } from "react-icons/lu";

import { useCapability } from "@hooks/stores";
import { useVirtualMicrophone } from "@hooks/VirtualMicrophoneProvider";
import { JsonRpcResponse, useJsonRpc } from "@hooks/useJsonRpc";
import { Button } from "@components/Button";
import Checkbox from "@components/Checkbox";
import { SettingsItem } from "@components/SettingsItem";
import { SettingsPageHeader } from "@components/SettingsPageheader";
import notifications from "@/notifications";
import { m } from "@localizations/messages.js";

function Dependency({ children }: { children: React.ReactNode }) {
  return (
    <p className="flex items-start gap-1.5 text-xs text-amber-700 dark:text-amber-300">
      <LuTriangleAlert className="mt-0.5 h-3.5 w-3.5 shrink-0" />
      <span>{children}</span>
    </p>
  );
}

export default function SettingsHardwareMicrophoneRoute() {
  const supported = useCapability("usb_virtual_microphone");
  const microphone = useVirtualMicrophone();
  const { send } = useJsonRpc();
  const [usbOn, setUsbOn] = useState<boolean | null>(null);
  const [busy, setBusy] = useState(false);
  const syncMicrophone = microphone.syncState;

  const load = useCallback(() => {
    send("getUsbDevices", {}, (resp: JsonRpcResponse) => {
      if (!("error" in resp)) setUsbOn(!!(resp.result as { microphone?: boolean }).microphone);
    });
  }, [send]);

  useEffect(() => {
    load();
    if (supported) void syncMicrophone();
  }, [load, supported, syncMicrophone]);

  const setUsb = (enabled: boolean) => {
    if (!enabled && microphone.running) microphone.stop();
    setBusy(true);
    send("setUsbDeviceState", { device: "microphone", enabled }, (resp: JsonRpcResponse) => {
      setBusy(false);
      if ("error" in resp) {
        notifications.error(
          m.usb_device_failed_set({ error: String(resp.error.data || m.unknown_error()) }),
        );
        return;
      }
      setUsbOn(enabled);
    });
  };

  const secure = window.isSecureContext;
  const canStart = supported && secure && !!usbOn;

  return (
    <div className="space-y-4">
      <SettingsPageHeader
        title={m.hardware_microphone_title()}
        description={m.hardware_microphone_description()}
      />
      {!supported && <Dependency>{m.hardware_microphone_unsupported()}</Dependency>}

      <SettingsItem
        loading={busy}
        title={m.hardware_microphone_usb_title()}
        description={m.hardware_microphone_usb_description()}
      >
        <Checkbox
          checked={!!usbOn}
          disabled={!supported || usbOn === null || busy}
          onChange={e => setUsb(e.target.checked)}
        />
      </SettingsItem>

      <div className="space-y-3">
        <div
          className="h-2 overflow-hidden rounded bg-slate-500/10"
          role="meter"
          aria-label={m.hardware_microphone_level()}
          aria-valuemin={0}
          aria-valuemax={100}
          aria-valuenow={Math.round(microphone.level * 100)}
        >
          <div
            className="h-full bg-emerald-500 transition-[width] duration-100"
            style={{ width: `${Math.round(Math.min(1, microphone.level) * 100)}%` }}
          />
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <Button
            size="SM"
            theme="primary"
            loading={microphone.busy}
            disabled={!canStart && !microphone.running}
            text={microphone.running ? m.hardware_microphone_stop() : m.hardware_microphone_start()}
            onClick={microphone.running ? microphone.stop : microphone.start}
          />
          <Button
            size="SM"
            theme="light"
            disabled={!microphone.running}
            text={microphone.muted ? m.hardware_microphone_unmute() : m.hardware_microphone_mute()}
            onClick={() => microphone.setMuted(!microphone.muted)}
          />
          <span className="text-xs text-slate-500">
            {microphone.state?.deviceReady
              ? m.hardware_microphone_ready()
              : m.hardware_microphone_waiting()}
            {" · "}
            {m.hardware_microphone_dropped({ count: microphone.state?.framesDropped ?? 0 })}
          </span>
        </div>
        {supported && usbOn === false && (
          <Dependency>{m.hardware_microphone_needs_usb()}</Dependency>
        )}
        {!secure && (
          <Dependency>
            {m.hardware_microphone_needs_https()}{" "}
            <a
              className="underline"
              href={`https://${window.location.hostname}${window.location.pathname}`}
            >
              {m.hardware_microphone_open_https()}
            </a>
          </Dependency>
        )}
        {(microphone.error || microphone.state?.lastError) && (
          <p role="alert" className="text-xs text-red-600">
            {microphone.error || microphone.state?.lastError}
          </p>
        )}
      </div>
    </div>
  );
}
