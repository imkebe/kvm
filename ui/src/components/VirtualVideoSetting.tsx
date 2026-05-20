import { useCallback, useEffect, useRef, useState } from "react";

import { Button } from "@components/Button";
import Fieldset from "@components/Fieldset";
import InputField from "@components/InputField";
import { SelectMenuBasic } from "@components/SelectMenuBasic";
import { SettingsItem } from "@components/SettingsItem";
import { SettingsSectionHeader } from "@components/SettingsSectionHeader";
import { useRTCStore } from "@hooks/stores";
import { JsonRpcResponse, useJsonRpc } from "@hooks/useJsonRpc";
import notifications from "@/notifications";

type VirtualVideoTransport = "auto" | "rtc-datachannel" | "webtransport-quic";

interface VirtualVideoConfig {
  enabled: boolean;
  transport: VirtualVideoTransport;
  device_path: string;
  width: number;
  height: number;
  fps: number;
  max_frame_bytes: number;
}

interface VirtualVideoState extends VirtualVideoConfig {
  streaming: boolean;
  frames_written: number;
  bytes_written: number;
  last_error?: string;
}

interface FrameSender {
  send: (payload: Uint8Array) => Promise<void>;
  close: () => void;
  transport: VirtualVideoTransport;
}

const defaultConfig: VirtualVideoConfig = {
  enabled: false,
  transport: "auto",
  device_path: "/dev/video1",
  width: 640,
  height: 480,
  fps: 15,
  max_frame_bytes: 2 * 1024 * 1024,
};

const frameMagic = 0x4a4b4d4a;
const frameVersion = 1;
const frameHeaderSize = 28;

type WebTransportConstructor = new (url: string) => {
  ready: Promise<void>;
  closed: Promise<void>;
  close: () => void;
  createUnidirectionalStream: () => Promise<WritableStream<Uint8Array>>;
};

function getWebTransportConstructor(): WebTransportConstructor | undefined {
  return (window as unknown as { WebTransport?: WebTransportConstructor }).WebTransport;
}

function encodeFrame(sequence: number, width: number, height: number, jpeg: Uint8Array) {
  const buffer = new ArrayBuffer(frameHeaderSize + jpeg.byteLength);
  const view = new DataView(buffer);
  const timestamp = BigInt(Date.now());
  view.setUint32(0, frameMagic);
  view.setUint16(4, frameVersion);
  view.setUint16(6, 0);
  view.setUint32(8, sequence);
  view.setUint32(12, Number(timestamp >> 32n));
  view.setUint32(16, Number(timestamp & 0xffffffffn));
  view.setUint16(20, width);
  view.setUint16(22, height);
  view.setUint32(24, jpeg.byteLength);
  new Uint8Array(buffer, frameHeaderSize).set(jpeg);
  return new Uint8Array(buffer);
}

async function canvasToJpeg(canvas: HTMLCanvasElement) {
  const blob = await new Promise<Blob | null>(resolve =>
    canvas.toBlob(resolve, "image/jpeg", 0.75),
  );
  if (!blob) throw new Error("Failed to encode camera frame");
  return new Uint8Array(await blob.arrayBuffer());
}

export function VirtualVideoSetting() {
  const { send } = useJsonRpc();
  const { peerConnection } = useRTCStore();
  const [config, setConfig] = useState<VirtualVideoConfig>(defaultConfig);
  const [state, setState] = useState<VirtualVideoState | null>(null);
  const [running, setRunning] = useState(false);
  const [busy, setBusy] = useState(false);
  const cleanupRef = useRef<(() => void) | null>(null);

  const syncState = useCallback(() => {
    send("getVirtualVideoState", {}, (resp: JsonRpcResponse) => {
      if ("error" in resp) return;
      const nextState = resp.result as VirtualVideoState;
      setState(nextState);
      setConfig({
        enabled: nextState.enabled,
        transport: nextState.transport || "auto",
        device_path: nextState.device_path || defaultConfig.device_path,
        width: nextState.width || defaultConfig.width,
        height: nextState.height || defaultConfig.height,
        fps: nextState.fps || defaultConfig.fps,
        max_frame_bytes: nextState.max_frame_bytes || defaultConfig.max_frame_bytes,
      });
    });
  }, [send]);

  useEffect(() => {
    syncState();
  }, [syncState]);

  const sendConfig = useCallback(
    async (nextConfig: VirtualVideoConfig) =>
      new Promise<void>((resolve, reject) => {
        send("setVirtualVideoConfig", { config: nextConfig }, (resp: JsonRpcResponse) => {
          if ("error" in resp) {
            reject(new Error(resp.error.data || resp.error.message));
            return;
          }
          setState(resp.result as VirtualVideoState);
          resolve();
        });
      }),
    [send],
  );

  const startRemote = useCallback(
    async () =>
      new Promise<void>((resolve, reject) => {
        send("startVirtualVideo", {}, (resp: JsonRpcResponse) => {
          if ("error" in resp) {
            reject(new Error(resp.error.data || resp.error.message));
            return;
          }
          setState(resp.result as VirtualVideoState);
          resolve();
        });
      }),
    [send],
  );

  const stopRemote = useCallback(
    () =>
      send("stopVirtualVideo", {}, (resp: JsonRpcResponse) => {
        if (!("error" in resp)) setState(resp.result as VirtualVideoState);
      }),
    [send],
  );

  const createRTCChannel = useCallback(async (): Promise<FrameSender> => {
    if (!peerConnection) throw new Error("Peer connection is not ready");
    const channel = peerConnection.createDataChannel("camera-mjpeg", {
      ordered: false,
      maxRetransmits: 0,
    });
    channel.binaryType = "arraybuffer";
    await new Promise<void>((resolve, reject) => {
      channel.onopen = () => resolve();
      channel.onerror = () => reject(new Error("Camera data channel failed"));
    });
    return {
      send: async (payload: Uint8Array) => {
        if (channel.bufferedAmount > 4 * 1024 * 1024) return;
        channel.send(payload);
      },
      close: () => channel.close(),
      transport: "rtc-datachannel" as VirtualVideoTransport,
    };
  }, [peerConnection]);

  const createWebTransportSender = useCallback(async (): Promise<FrameSender> => {
    const WebTransportCtor = getWebTransportConstructor();
    if (!WebTransportCtor) throw new Error("WebTransport is not supported by this browser");
    const transport = new WebTransportCtor(
      `https://${window.location.hostname}:9443/virtual-video/webtransport`,
    );
    await transport.ready;
    return {
      send: async (payload: Uint8Array) => {
        const stream = await transport.createUnidirectionalStream();
        const writer = stream.getWriter();
        await writer.write(payload);
        await writer.close();
      },
      close: () => transport.close(),
      transport: "webtransport-quic" as VirtualVideoTransport,
    };
  }, []);

  const start = useCallback(async () => {
    setBusy(true);
    try {
      const nextConfig = { ...config, enabled: true };
      await sendConfig(nextConfig);
      await startRemote();

      const stream = await navigator.mediaDevices.getUserMedia({
        video: {
          width: { ideal: nextConfig.width },
          height: { ideal: nextConfig.height },
          frameRate: { ideal: nextConfig.fps },
        },
        audio: false,
      });

      const video = document.createElement("video");
      video.muted = true;
      video.playsInline = true;
      video.srcObject = stream;
      await video.play();

      const canvas = document.createElement("canvas");
      canvas.width = nextConfig.width;
      canvas.height = nextConfig.height;
      const ctx = canvas.getContext("2d");
      if (!ctx) throw new Error("Failed to create camera frame encoder");

      let sender: FrameSender;
      if (nextConfig.transport === "webtransport-quic" || nextConfig.transport === "auto") {
        try {
          sender = await createWebTransportSender();
        } catch (error) {
          if (nextConfig.transport === "webtransport-quic") throw error;
          sender = await createRTCChannel();
        }
      } else {
        sender = await createRTCChannel();
      }

      let sequence = 0;
      let frameInFlight = false;
      const interval = window.setInterval(
        async () => {
          if (frameInFlight) return;
          frameInFlight = true;
          try {
            ctx.drawImage(video, 0, 0, nextConfig.width, nextConfig.height);
            const jpeg = await canvasToJpeg(canvas);
            await sender.send(encodeFrame(sequence++, nextConfig.width, nextConfig.height, jpeg));
          } catch (error) {
            console.warn("Failed to send virtual camera frame", error);
          } finally {
            frameInFlight = false;
          }
        },
        Math.max(1000 / nextConfig.fps, 1),
      );

      cleanupRef.current = () => {
        window.clearInterval(interval);
        sender.close();
        stream.getTracks().forEach(track => track.stop());
      };
      setRunning(true);
      notifications.success(`Virtual camera started using ${sender.transport}`);
    } catch (error) {
      cleanupRef.current?.();
      cleanupRef.current = null;
      stopRemote();
      notifications.error(
        error instanceof Error ? error.message : "Failed to start virtual camera",
      );
    } finally {
      setBusy(false);
    }
  }, [config, createRTCChannel, createWebTransportSender, sendConfig, startRemote, stopRemote]);

  const stop = useCallback(() => {
    cleanupRef.current?.();
    cleanupRef.current = null;
    stopRemote();
    setRunning(false);
    notifications.success("Virtual camera stopped");
  }, [stopRemote]);

  useEffect(() => () => cleanupRef.current?.(), []);

  return (
    <Fieldset disabled={busy} className="space-y-4">
      <div className="h-px w-full bg-slate-800/10 dark:bg-slate-300/20" />
      <SettingsSectionHeader
        title="Virtual Camera"
        description="Stream this browser's camera to the target as a USB video device"
      />
      <SettingsItem
        title="Transport"
        description="Automatic falls back to WebRTC if QUIC is unavailable"
      >
        <SelectMenuBasic
          size="SM"
          value={config.transport}
          onChange={e =>
            setConfig(prev => ({ ...prev, transport: e.target.value as VirtualVideoTransport }))
          }
          options={[
            { value: "auto", label: "Automatic" },
            { value: "rtc-datachannel", label: "WebRTC data channel" },
            { value: "webtransport-quic", label: "WebTransport over QUIC" },
          ]}
        />
      </SettingsItem>
      <SettingsItem title="Resolution" description="MJPEG frame size sent to the USB video device">
        <div className="flex gap-2">
          <InputField
            size="SM"
            type="number"
            value={config.width}
            onChange={e => setConfig(prev => ({ ...prev, width: Number(e.target.value) }))}
          />
          <InputField
            size="SM"
            type="number"
            value={config.height}
            onChange={e => setConfig(prev => ({ ...prev, height: Number(e.target.value) }))}
          />
        </div>
      </SettingsItem>
      <SettingsItem title="Device Path" description="V4L2 output device exposed by the UVC gadget">
        <InputField
          size="SM"
          value={config.device_path}
          onChange={e => setConfig(prev => ({ ...prev, device_path: e.target.value }))}
        />
      </SettingsItem>
      <SettingsItem title="Frame Rate" description="Frames per second encoded by the browser">
        <InputField
          size="SM"
          type="number"
          value={config.fps}
          onChange={e => setConfig(prev => ({ ...prev, fps: Number(e.target.value) }))}
        />
      </SettingsItem>
      <div className="flex gap-x-2">
        <Button
          size="SM"
          loading={busy}
          theme="primary"
          text={running ? "Stop Virtual Camera" : "Start Virtual Camera"}
          onClick={running ? stop : start}
        />
        <Button size="SM" theme="light" text="Refresh State" onClick={syncState} />
      </div>
      {state?.last_error && (
        <p className="text-xs text-red-700 dark:text-red-300">{state.last_error}</p>
      )}
    </Fieldset>
  );
}
