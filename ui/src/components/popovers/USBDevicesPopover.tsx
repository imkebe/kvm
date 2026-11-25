import { useCallback, useEffect, useRef, useState } from "react";
import {
  Line,
  LineChart,
  ResponsiveContainer,
  CartesianGrid,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";
import { Button } from "@components/Button";
import { GridCard } from "@components/Card";
import {
  CameraDevicePreview,
  CameraSourceSelect,
  CameraCodecSelect,
  CameraFormatSelect,
} from "@components/CameraDevicePreview";
import { useCameraWatch, useVirtualCamera } from "@hooks/VirtualCameraProvider";
import { useVirtualMicrophone } from "@hooks/VirtualMicrophoneProvider";
import { useCapability, type UsbDeviceMenu } from "@hooks/stores";
import { useDeviceRpc } from "@hooks/useDeviceRpc";
import { useDeviceUiNavigation } from "@hooks/useAppNavigation";
import {
  formatUSBBytes,
  usbTrafficRate,
  type USBNetworkSample,
  type USBTrafficRate,
} from "@/utils/usbNetworkMetrics";
import notifications from "@/notifications";

interface USBClasses {
  video: boolean;
  network: boolean;
  microphone?: boolean;
}
interface NetworkState extends USBNetworkSample {
  running: boolean;
  link_up: boolean;
  mode: string;
  function: string;
  address_cidr: string;
  last_error?: string;
}

const titles: Record<UsbDeviceMenu, [string, string]> = {
  camera: ["USB Camera", "This browser's camera, presented to your target computer"],
  microphone: ["USB Microphone", "This browser's microphone, presented to your target computer"],
  network: ["USB Ethernet", "A network link between the KVM and your target computer"],
};

function megabits(bitsPerSecond: number) {
  return bitsPerSecond >= 1e6
    ? `${(bitsPerSecond / 1e6).toFixed(1)} Mbit/s`
    : `${Math.round(bitsPerSecond / 1000)} kbit/s`;
}

// The frame size and rate at each step from the browser's camera to the
// target computer.
function CameraFormat() {
  useCameraWatch();
  const { state, stats, usbFps, running } = useVirtualCamera();
  const rows: [string, string][] = [
    [
      "Target asked for",
      state?.enabled
        ? `${state.width}×${state.height} · ${state.fps} fps · ${state.codec.toUpperCase()}`
        : "The target has not opened the camera",
    ],
    [
      "Browser camera",
      stats?.sourceWidth
        ? `${stats.sourceWidth}×${stats.sourceHeight}${stats.sourceFps ? ` · ${Math.round(stats.sourceFps)} fps` : ""}`
        : "—",
    ],
    [
      "Sent to KVM",
      running && stats
        ? `${stats.width}×${stats.height} · ${stats.fps.toFixed(1)} fps · ${megabits(stats.bitsPerSecond)} · ${stats.transport}`
        : "—",
    ],
    [
      "KVM to target over USB",
      state?.usb_streaming ? `${usbFps.toFixed(1)} fps` : "The target is not reading the camera",
    ],
  ];
  return (
    <dl
      className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 rounded bg-slate-500/5 p-2 text-xs"
      aria-label="Camera frame size and rate"
    >
      {rows.map(([name, value]) => (
        <div key={name} className="contents">
          <dt className="text-slate-500">{name}</dt>
          <dd className="font-medium tabular-nums">{value}</dd>
        </div>
      ))}
    </dl>
  );
}

export default function USBDevicesPopover({
  device,
  onClose,
}: {
  device: UsbDeviceMenu;
  onClose: () => void;
}) {
  const { request, ready } = useDeviceRpc();
  const { navigateTo } = useDeviceUiNavigation();
  const camera = useVirtualCamera();
  const microphone = useVirtualMicrophone();
  const syncMicrophone = microphone.syncState;
  const microphoneSupported = useCapability("usb_virtual_microphone");
  const [devices, setDevices] = useState<USBClasses | null>(null);
  const [network, setNetwork] = useState<NetworkState | null>(null);
  const [traffic, setTraffic] = useState<USBTrafficRate[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const previous = useRef<USBNetworkSample | null>(null);
  const pending = useRef(false);
  const mounted = useRef(true);

  const refresh = useCallback(async () => {
    if (!ready || pending.current) return;
    pending.current = true;
    try {
      const [classes, next] = await Promise.all([
        request<USBClasses>("getUsbDevices"),
        request<NetworkState>("getUsbNetworkState"),
      ]);
      if (!mounted.current) return;
      if (microphoneSupported) void syncMicrophone();
      setDevices(classes);
      setNetwork(next);
      setError("");
      const rate = usbTrafficRate(previous.current, next);
      if (rate)
        setTraffic(points =>
          [...points, rate].filter(point => next.sampled_at - point.date <= 60000),
        );
      else if (
        !next.stats_available ||
        (previous.current &&
          (next.sampled_at - previous.current.sampled_at > 10000 ||
            next.interface_name !== previous.current.interface_name ||
            next.rx_bytes < previous.current.rx_bytes ||
            next.tx_bytes < previous.current.tx_bytes))
      )
        setTraffic([]);
      previous.current = next;
    } catch (failure) {
      if (mounted.current)
        setError(failure instanceof Error ? failure.message : "Device unavailable");
    } finally {
      pending.current = false;
    }
  }, [ready, request, microphoneSupported, syncMicrophone]);

  useEffect(() => {
    mounted.current = true;
    previous.current = null;
    setTraffic([]);
    void refresh();
    const timer = window.setInterval(() => void refresh(), 1000);
    return () => {
      mounted.current = false;
      window.clearInterval(timer);
    };
  }, [refresh]);

  const act = async (action: () => Promise<unknown>) => {
    setBusy(true);
    try {
      await action();
      await refresh();
      camera.syncState();
    } catch (failure) {
      notifications.error(failure instanceof Error ? failure.message : "USB action failed");
    } finally {
      if (mounted.current) setBusy(false);
    }
  };

  const toggleClass = (device: "video" | "network" | "microphone", enabled: boolean) => {
    void act(async () => {
      if (device === "video" && !enabled && camera.running) camera.stop();
      if (device === "microphone" && !enabled && microphone.running) microphone.stop();
      await request("setUsbDeviceState", { device, enabled });
      setDevices(previous => (previous ? { ...previous, [device]: enabled } : previous));
    });
  };

  const latest = traffic.at(-1);
  const disabled = !ready || busy || !devices;
  return (
    <GridCard cardClassName="border border-solid border-slate-800/30 shadow-lg outline-none dark:border-slate-300/30">
      <div className="max-h-[calc(100dvh-150px)] space-y-5 overflow-y-auto p-4 text-slate-900 dark:text-slate-100">
        <div className="flex items-start justify-between gap-3">
          <div>
            <h2 className="text-base font-semibold">{titles[device][0]}</h2>
            <p className="text-sm text-slate-500">{titles[device][1]}</p>
          </div>
          <Button
            size="XS"
            theme="light"
            text="Close"
            aria-label={`Close ${titles[device][0]}`}
            onClick={onClose}
          />
        </div>
        {error && (
          <p role="alert" className="text-sm text-red-600 dark:text-red-300">
            {error}
          </p>
        )}
        {device === "camera" && (
          <section className="space-y-3" aria-label="USB camera controls">
            <div className="flex items-center justify-between gap-3">
              <p className="text-xs text-slate-500">
                {devices?.video ? "The target sees a USB camera" : "The USB camera is switched off"}
              </p>
              <Button
                size="XS"
                theme={devices?.video ? "primary" : "light"}
                text={devices?.video ? "Disable USB Camera" : "Enable USB Camera"}
                disabled={disabled || camera.busy}
                onClick={() => toggleClass("video", !devices?.video)}
              />
            </div>
            <CameraSourceSelect />
            <CameraCodecSelect />
            <CameraFormatSelect />
            <CameraFormat />
            <CameraDevicePreview />
            {!window.isSecureContext && (
              <p className="text-xs text-amber-700 dark:text-amber-300">
                Camera access needs HTTPS.{" "}
                <a
                  className="underline"
                  href={`https://${window.location.hostname}${window.location.pathname}`}
                >
                  Open secure device view
                </a>
              </p>
            )}
            <div className="flex items-center justify-between gap-3">
              <Button
                size="SM"
                theme="primary"
                loading={camera.busy}
                disabled={disabled}
                text={camera.running ? "Stop Camera" : "Start Camera"}
                onClick={camera.running ? camera.stop : camera.start}
              />
              <span className="text-xs text-slate-500">
                {camera.state?.frames_written ?? 0} received · {camera.state?.usb_frames_sent ?? 0}{" "}
                sent over USB
              </span>
            </div>
            {camera.state?.last_error && (
              <p role="alert" className="text-xs text-red-600">
                {camera.state.last_error}
              </p>
            )}
          </section>
        )}
        {device === "microphone" && microphoneSupported && (
          <section className="space-y-3" aria-label="USB microphone controls">
            <div className="flex items-center justify-between gap-3">
              <p className="text-xs text-slate-500">
                {microphone.running
                  ? microphone.muted
                    ? "Muted"
                    : "Sending this browser's microphone"
                  : "Microphone sending is stopped"}
              </p>
              <Button
                size="XS"
                theme={devices?.microphone ? "primary" : "light"}
                text={devices?.microphone ? "Disable USB Microphone" : "Enable USB Microphone"}
                disabled={disabled || microphone.busy}
                onClick={() => toggleClass("microphone", !devices?.microphone)}
              />
            </div>
            <div
              className="h-2 overflow-hidden rounded bg-slate-500/10"
              role="meter"
              aria-label="Microphone level"
              aria-valuemin={0}
              aria-valuemax={100}
              aria-valuenow={Math.round(microphone.level * 100)}
            >
              <div
                className="h-full bg-emerald-500 transition-[width] duration-100"
                style={{
                  width: `${Math.round(Math.min(1, microphone.level) * 100)}%`,
                }}
              />
            </div>
            <div className="flex items-center justify-between gap-3">
              <div className="flex gap-2">
                <Button
                  size="SM"
                  theme="primary"
                  loading={microphone.busy}
                  disabled={disabled}
                  text={microphone.running ? "Stop Microphone" : "Start Microphone"}
                  onClick={microphone.running ? microphone.stop : microphone.start}
                />
                <Button
                  size="SM"
                  theme="light"
                  disabled={!microphone.running}
                  text={microphone.muted ? "Unmute" : "Mute"}
                  onClick={() => microphone.setMuted(!microphone.muted)}
                />
              </div>
              <span className="text-xs text-slate-500">
                {microphone.state?.deviceReady ? "Device ready" : "Waiting for target"} ·{" "}
                {microphone.state?.framesDropped ?? 0} dropped
              </span>
            </div>
            {!window.isSecureContext && (
              <p className="text-xs text-amber-700 dark:text-amber-300">
                Microphone access needs HTTPS.{" "}
                <a
                  className="underline"
                  href={`https://${window.location.hostname}${window.location.pathname}`}
                >
                  Open secure device view
                </a>
              </p>
            )}
            {(microphone.error || microphone.state?.lastError) && (
              <p role="alert" className="text-xs text-red-600">
                {microphone.error || microphone.state?.lastError}
              </p>
            )}
          </section>
        )}
        {device === "network" && (
          <section className="space-y-3" aria-label="USB network controls">
            <div className="flex items-center justify-between gap-3">
              <p className="text-xs text-slate-500">
                {network?.link_up ? "Target connected" : "No USB link"} ·{" "}
                {network?.function?.toUpperCase() || "NCM"}
              </p>
              <Button
                size="XS"
                theme={devices?.network ? "primary" : "light"}
                text={devices?.network ? "Disable USB Ethernet" : "Enable USB Ethernet"}
                disabled={disabled || camera.busy}
                onClick={() => toggleClass("network", !devices?.network)}
              />
            </div>
            <div className="flex items-center justify-between gap-3">
              <Button
                size="SM"
                theme="light"
                disabled={disabled}
                text={network?.running ? "Stop Network Sharing" : "Start Network Sharing"}
                onClick={() =>
                  void act(async () => {
                    if (!devices?.network)
                      await request("setUsbDeviceState", {
                        device: "network",
                        enabled: true,
                      });
                    await request(network?.running ? "stopUsbNetwork" : "startUsbNetwork");
                  })
                }
              />
              <span className="text-xs text-slate-500">
                {network?.running
                  ? `${network.mode.toUpperCase()} · ${network.address_cidr}`
                  : "Sharing stopped"}
              </span>
            </div>
            <div className="grid grid-cols-2 gap-3 text-sm">
              <div>
                <span className="text-xs text-slate-500">From target</span>
                <p className="font-semibold text-blue-600">
                  {latest ? `${formatUSBBytes(latest.fromTarget)}/s` : "—"}
                </p>
              </div>
              <div>
                <span className="text-xs text-slate-500">To target</span>
                <p className="font-semibold text-emerald-600">
                  {latest ? `${formatUSBBytes(latest.toTarget)}/s` : "—"}
                </p>
              </div>
            </div>
            <div
              className="h-36"
              role="img"
              aria-label="Live USB Ethernet traffic chart, bytes per second over the last 60 seconds"
            >
              {network?.stats_available && traffic.length > 1 ? (
                <ResponsiveContainer width="100%" height="100%">
                  <LineChart data={traffic} margin={{ top: 8, right: 8, bottom: 0, left: 0 }}>
                    <CartesianGrid vertical={false} strokeDasharray="3 3" />
                    <XAxis
                      dataKey="date"
                      type="number"
                      domain={["dataMin", "dataMax"]}
                      tickFormatter={date =>
                        `${Math.round((date - (traffic.at(-1)?.date || date)) / 1000)}s`
                      }
                      tick={{ fontSize: 10 }}
                    />
                    <YAxis
                      width={58}
                      domain={[0, "auto"]}
                      tickFormatter={formatUSBBytes}
                      tick={{ fontSize: 10 }}
                    />
                    <Tooltip
                      labelFormatter={() => "USB traffic"}
                      formatter={value => `${formatUSBBytes(Number(value))}/s`}
                    />
                    <Line
                      dataKey="fromTarget"
                      name="From target"
                      stroke="#2563eb"
                      dot={false}
                      isAnimationActive={false}
                    />
                    <Line
                      dataKey="toTarget"
                      name="To target"
                      stroke="#059669"
                      dot={false}
                      isAnimationActive={false}
                    />
                  </LineChart>
                </ResponsiveContainer>
              ) : (
                <div className="flex h-full items-center justify-center rounded bg-slate-500/5 text-xs text-slate-500">
                  {network?.stats_available
                    ? "Collecting live traffic samples…"
                    : "Traffic counters unavailable"}
                </div>
              )}
            </div>
            <p className="text-xs text-slate-500">
              Interface totals: {formatUSBBytes(network?.rx_bytes ?? 0)} from target ·{" "}
              {formatUSBBytes(network?.tx_bytes ?? 0)} to target. Sampled every second.
            </p>
            {network?.last_error && (
              <p role="alert" className="text-xs text-red-600">
                {network.last_error}
              </p>
            )}
          </section>
        )}
        <div className="flex gap-2 border-t border-slate-300/30 pt-3">
          <Button
            size="XS"
            theme="light"
            text={
              device === "network"
                ? "Ethernet Settings"
                : device === "microphone"
                  ? "Microphone Settings"
                  : "Camera Settings"
            }
            onClick={() => {
              onClose();
              navigateTo(
                `/settings/hardware/${device === "network" ? "network" : device === "microphone" ? "microphone" : "camera"}`,
              );
            }}
          />
        </div>
      </div>
    </GridCard>
  );
}
