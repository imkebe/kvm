import {
  createContext,
  useContext,
  useCallback,
  useEffect,
  useRef,
  useState,
  type PropsWithChildren,
} from "react";
import { useCapability, useRTCStore } from "@hooks/stores";
import { useJsonRpc, type JsonRpcResponse } from "@hooks/useJsonRpc";
import { useDeviceRpc } from "@hooks/useDeviceRpc";
import notifications from "@/notifications";

export type VirtualVideoCodec = "mjpeg" | "h264";
export type VirtualVideoTransport = "auto" | "rtc-datachannel" | "websocket";

export interface VirtualVideoConfig {
  enabled: boolean;
  codec: VirtualVideoCodec;
  transport: VirtualVideoTransport;
  device_path: string;
  width: number;
  height: number;
  fps: number;
  // The one frame size (a key of usbFrameSizes) and rate (one of
  // usbFrameRates) that the MJPEG USB camera offers. Empty and 0: the target
  // computer picks.
  usb_size: string;
  usb_fps: number;
}

export interface VirtualVideoState extends VirtualVideoConfig {
  streaming: boolean;
  usb_streaming: boolean;
  usb_frames_sent: number;
  needs_keyframe: boolean;
  dropped_frames: number;
  frames_written: number;
  bytes_written: number;
  last_error?: string;
}

// What the browser's camera gives and what goes to the KVM, measured over
// the last second.
export interface CameraStats {
  sourceWidth: number;
  sourceHeight: number;
  sourceFps: number;
  width: number;
  height: number;
  fps: number;
  bitsPerSecond: number;
  transport: VirtualVideoTransport;
}

interface FrameSender {
  send: (payload: Uint8Array) => Promise<void>;
  close: () => void;
  canSend: () => boolean;
  transport: VirtualVideoTransport;
}

const defaultConfig: VirtualVideoConfig = {
  enabled: false,
  codec: "mjpeg",
  transport: "auto",
  device_path: "auto",
  width: 640,
  height: 480,
  fps: 15,
  usb_size: "",
  usb_fps: 0,
};

const frameMagic = 0x4a4b4d4a;
const frameVersion = 1;
const frameHeaderSize = 28;

function encodeFrame(sequence: number, width: number, height: number, jpeg: Uint8Array, flags = 0) {
  const buffer = new ArrayBuffer(frameHeaderSize + jpeg.byteLength);
  const view = new DataView(buffer);
  const timestamp = BigInt(Date.now());
  view.setUint32(0, frameMagic);
  view.setUint16(4, flags ? 2 : frameVersion);
  view.setUint16(6, flags);
  view.setUint32(8, sequence);
  view.setUint32(12, Number(timestamp >> 32n));
  view.setUint32(16, Number(timestamp & 0xffffffffn));
  view.setUint16(20, width);
  view.setUint16(22, height);
  view.setUint32(24, jpeg.byteLength);
  new Uint8Array(buffer, frameHeaderSize).set(jpeg);
  return new Uint8Array(buffer);
}

// About 2.4 Mbit/s: a third of what the KVM can take.
const MJPEG_BYTES_PER_SECOND = 300 * 1024;
// Over the camera WebSocket the KVM takes 12 Mbit/s with about a third of
// its processor.
const MJPEG_FAST_BYTES_PER_SECOND = 1536 * 1024;

async function canvasToJpeg(canvas: HTMLCanvasElement) {
  const quality = 0.6;
  const blob = await new Promise<Blob | null>(resolve =>
    canvas.toBlob(resolve, "image/jpeg", quality),
  );
  if (!blob) throw new Error("Failed to encode camera frame");
  return new Uint8Array(await blob.arrayBuffer());
}

function useCameraController() {
  const { send } = useJsonRpc();
  const { peerConnection, rpcDataChannel } = useRTCStore();
  const [config, setConfig] = useState<VirtualVideoConfig>(defaultConfig);
  const [state, setState] = useState<VirtualVideoState | null>(null);
  const [running, setRunning] = useState(false);
  const [busy, setBusy] = useState(false);
  const [stats, setStats] = useState<CameraStats | null>(null);
  // Frames a second that the KVM gives the target over USB.
  const [usbFps, setUsbFps] = useState(0);
  const usbSample = useRef({ frames: 0, at: 0 });
  // Panels that show live camera state register here; with none open and the camera off, nothing polls.
  const [watchers, setWatchers] = useState(0);
  const watch = useCallback(() => {
    setWatchers(n => n + 1);
    return () => setWatchers(n => n - 1);
  }, []);
  const capable = useCapability("usb_virtual_devices");
  const cleanupRef = useRef<(() => void) | null>(null);
  const sessionRef = useRef(0);
  const startingRef = useRef(false);
  const stateRef = useRef<VirtualVideoState | null>(null);
  stateRef.current = state;
  const [sourceId, setSourceId] = useState("");
  const [sources, setSources] = useState<MediaDeviceInfo[]>([]);
  const { request } = useDeviceRpc();
  // The KVM's own USB camera, seen as a source when the browser runs on the
  // target, is named by its function name (default "UVC Camera").
  const kvmCameraName = useRef("UVC Camera");
  const isKvmCamera = useCallback(
    (label: string) =>
      /UVC Camera/i.test(label) ||
      label.toLowerCase().includes(kvmCameraName.current.toLowerCase()),
    [],
  );
  const refreshSources = useCallback(async () => {
    if (!navigator.mediaDevices) return;
    try {
      const names = await request<{ camera_name?: string }>("getUsbFunctionNames");
      kvmCameraName.current = names.camera_name || "UVC Camera";
    } catch {
      /* Older firmware: keep the default name. */
    }
    const devices = await navigator.mediaDevices.enumerateDevices();
    setSources(
      devices.filter(device => device.kind === "videoinput" && !isKvmCamera(device.label)),
    );
  }, [request, isKvmCamera]);

  const syncState = useCallback(() => {
    send("getVirtualVideoState", {}, (resp: JsonRpcResponse) => {
      if ("error" in resp) return;
      const nextState = resp.result as VirtualVideoState;
      setState(nextState);
      setConfig(prev => ({
        enabled: nextState.enabled,
        codec: nextState.codec || "mjpeg",
        // state.transport is the last one used, not the choice: keep the choice.
        transport: prev.transport,
        device_path: defaultConfig.device_path, // the KVM picks the device itself
        width: nextState.width || defaultConfig.width,
        height: nextState.height || defaultConfig.height,
        fps: nextState.fps || defaultConfig.fps,
        usb_size: nextState.usb_size || "",
        usb_fps: nextState.usb_fps || 0,
      }));
    });
  }, [send]);

  useEffect(() => {
    if (rpcDataChannel?.readyState === "open") syncState();
  }, [rpcDataChannel, syncState]);

  const polling = capable && (watchers > 0 || running || !!state?.enabled);
  useEffect(() => {
    if (rpcDataChannel?.readyState !== "open" || !polling) return;
    const timer = window.setInterval(() => {
      send("getVirtualVideoState", {}, response => {
        if ("error" in response) return;
        const next = response.result as VirtualVideoState;
        const now = performance.now();
        const last = usbSample.current;
        const frames = next.usb_frames_sent ?? 0;
        if (last.at && frames >= last.frames && now > last.at)
          setUsbFps(((frames - last.frames) * 1000) / (now - last.at));
        else setUsbFps(0);
        usbSample.current = { frames, at: now };
        if (!startingRef.current) setState(next);
      });
    }, 1000);
    return () => window.clearInterval(timer);
  }, [rpcDataChannel, send, polling]);

  const sendConfig = useCallback(
    async (nextConfig: VirtualVideoConfig) => {
      setState(
        await request<VirtualVideoState>("setVirtualVideoConfig", {
          config: nextConfig,
        }),
      );
    },
    [request],
  );

  // Sets the frame size and rate that the USB camera offers. The KVM makes
  // the camera's descriptors again, which reconnects its USB devices.
  const setUsbFormat = useCallback(
    async (format: { usb_size?: string; usb_fps?: number }) => {
      const next = { ...config, ...format, enabled: false };
      setBusy(true);
      try {
        await sendConfig(next);
        setConfig(next);
      } catch (error) {
        notifications.error(
          error instanceof Error ? error.message : "Failed to change the camera format",
        );
      } finally {
        setBusy(false);
      }
    },
    [config, sendConfig],
  );

  const startRemote = useCallback(async () => {
    setState(await request<VirtualVideoState>("startVirtualVideo"));
  }, [request]);

  const stopRemote = useCallback(() => {
    void request<VirtualVideoState>("stopVirtualVideo")
      .then(setState)
      .catch(() => {});
  }, [request]);

  const createRTCChannel = useCallback(
    async (codec: VirtualVideoCodec = "mjpeg"): Promise<FrameSender> => {
      if (!peerConnection) throw new Error("Peer connection is not ready");
      const channel = peerConnection.createDataChannel(
        codec === "h264" ? "camera-h264" : "camera-mjpeg",
        codec === "h264" ? { ordered: true } : { ordered: false, maxRetransmits: 0 },
      );
      channel.binaryType = "arraybuffer";
      await new Promise<void>((resolve, reject) => {
        const timer = window.setTimeout(() => {
          channel.close();
          reject(new Error("Camera data channel timed out"));
        }, 10000);
        channel.onopen = () => {
          window.clearTimeout(timer);
          resolve();
        };
        channel.onerror = () => {
          window.clearTimeout(timer);
          channel.close();
          reject(new Error("Camera data channel failed"));
        };
      });
      return {
        send: async (payload: Uint8Array) => {
          if (channel.readyState !== "open") throw new Error("Camera connection closed");
          if (channel.bufferedAmount > 1024 * 1024 && codec === "h264")
            throw new Error("H.264 connection is too slow; restart or choose MJPEG");
          if (channel.bufferedAmount > 256 * 1024 && codec !== "h264") return;
          channel.send(new Uint8Array(payload).buffer);
        },
        close: () => channel.close(),
        canSend: () => channel.readyState === "open" && channel.bufferedAmount < 256 * 1024,
        transport: "rtc-datachannel" as VirtualVideoTransport,
      };
    },
    [peerConnection],
  );

  // The camera WebSocket goes to the KVM over TCP, which costs the KVM's
  // processor much less than the data channel, so 720p and 1080p frames fit.
  // It needs a direct connection to the KVM: a cloud session has none.
  const createWebSocketSender = useCallback(async (): Promise<FrameSender> => {
    const scheme = window.location.protocol === "https:" ? "wss" : "ws";
    const socket = new WebSocket(`${scheme}://${window.location.host}/virtual-video/ws`);
    socket.binaryType = "arraybuffer";
    await new Promise<void>((resolve, reject) => {
      const timer = window.setTimeout(() => {
        socket.close();
        reject(new Error("Camera WebSocket timed out"));
      }, 4000);
      socket.onopen = () => {
        window.clearTimeout(timer);
        resolve();
      };
      socket.onerror = () => {
        window.clearTimeout(timer);
        reject(new Error("Camera WebSocket failed"));
      };
    });
    return {
      send: async (payload: Uint8Array) => {
        if (socket.readyState !== WebSocket.OPEN) throw new Error("Camera connection closed");
        if (socket.bufferedAmount > 512 * 1024) return;
        socket.send(new Uint8Array(payload).buffer);
      },
      close: () => socket.close(),
      canSend: () => socket.readyState === WebSocket.OPEN && socket.bufferedAmount < 512 * 1024,
      transport: "websocket" as VirtualVideoTransport,
    };
  }, []);

  const start = useCallback(async () => {
    if (startingRef.current || cleanupRef.current) return;
    startingRef.current = true;
    const session = ++sessionRef.current;
    const checkSession = () => {
      if (session !== sessionRef.current) throw new Error("Camera start cancelled");
    };
    setBusy(true);
    try {
      if (!navigator.mediaDevices?.getUserMedia) {
        throw new Error(
          "Camera access requires HTTPS. Open this device over HTTPS and accept its certificate before starting the camera.",
        );
      }
      const devices = await request<{ video: boolean }>("getUsbDevices");
      if (!devices.video)
        await request("setUsbDeviceState", {
          device: "video",
          enabled: true,
        });
      checkSession();
      const nextConfig: VirtualVideoConfig = {
        ...config,
        device_path: "auto",
        enabled: true,
      };
      // The H.264 level and the bitrate follow the frame size. The KVM
      // relays about 3 Mbit/s of camera frames without slowing the mouse.
      const tall = nextConfig.height >= 1080;
      const medium = nextConfig.height >= 720;
      const h264Config: VideoEncoderConfig = {
        codec: tall ? "avc1.42E028" : medium ? "avc1.42E01F" : "avc1.42E01E",
        width: nextConfig.width,
        height: nextConfig.height,
        framerate: nextConfig.fps,
        bitrate: 1_500_000,
        latencyMode: "realtime",
        avc: { format: "annexb" },
      };
      if (nextConfig.codec === "h264") {
        let supported = false;
        try {
          supported =
            typeof VideoEncoder !== "undefined" &&
            !!(await VideoEncoder.isConfigSupported(h264Config)).supported;
        } catch {
          /* Unsupported encoder configuration falls back to MJPEG. */
        }
        if (!supported) {
          nextConfig.codec = "mjpeg";
          notifications.error("This browser cannot encode H.264. Using MJPEG instead.");
        } else nextConfig.transport = "rtc-datachannel";
      }
      setConfig(nextConfig);

      const stream = await navigator.mediaDevices.getUserMedia({
        video: {
          ...(sourceId ? { deviceId: { exact: sourceId } } : {}),
          // The USB host picks the MJPEG size, up to 1080p: a larger source
          // is scaled down for it.
          width: { ideal: nextConfig.codec === "mjpeg" ? 1920 : 640 },
          height: { ideal: nextConfig.codec === "mjpeg" ? 1080 : 480 },
          frameRate: { ideal: nextConfig.codec === "mjpeg" ? 30 : 15 },
        },
        audio: false,
      });

      if (session !== sessionRef.current) {
        stream.getTracks().forEach(track => track.stop());
        return;
      }
      cleanupRef.current = () => stream.getTracks().forEach(track => track.stop());
      await refreshSources();
      checkSession();
      if (isKvmCamera(stream.getVideoTracks()[0]?.label || "")) {
        throw new Error(
          "Choose a laptop camera source to avoid feeding the JetKVM USB camera back into itself.",
        );
      }
      await sendConfig(nextConfig);
      checkSession();
      await startRemote();
      checkSession();

      const video = document.createElement("video");
      video.muted = true;
      video.playsInline = true;
      video.srcObject = stream;
      await video.play();
      checkSession();

      const canvas = document.createElement("canvas");
      canvas.width = nextConfig.width;
      canvas.height = nextConfig.height;
      const ctx = canvas.getContext("2d");
      if (!ctx) throw new Error("Failed to create camera frame encoder");

      let sender: FrameSender | undefined;
      if (
        nextConfig.codec === "mjpeg" &&
        (nextConfig.transport === "auto" || nextConfig.transport === "websocket")
      ) {
        try {
          sender = await createWebSocketSender();
        } catch (error) {
          if (nextConfig.transport === "websocket") throw error;
        }
      }
      if (sender) {
        /* The WebSocket is open. */
      } else {
        sender = await createRTCChannel(nextConfig.codec);
      }
      if (session !== sessionRef.current) {
        sender.close();
        stream.getTracks().forEach(track => track.stop());
        return;
      }
      cleanupRef.current = () => {
        sender.close();
        stream.getTracks().forEach(track => track.stop());
      };

      let sequence = 0;
      let sentFrames = 0;
      let sentBytes = 0;
      let statsAt = performance.now();
      const statsTimer = window.setInterval(() => {
        if (session !== sessionRef.current) return;
        const now = performance.now();
        const seconds = Math.max((now - statsAt) / 1000, 0.001);
        const source = stream.getVideoTracks()[0]?.getSettings();
        setStats({
          sourceWidth: video.videoWidth || source?.width || 0,
          sourceHeight: video.videoHeight || source?.height || 0,
          sourceFps: source?.frameRate || 0,
          width: canvas.width,
          height: canvas.height,
          fps: sentFrames / seconds,
          bitsPerSecond: (sentBytes * 8) / seconds,
          transport: sender.transport,
        });
        sentFrames = 0;
        sentBytes = 0;
        statsAt = now;
      }, 1000);
      cleanupRef.current = () => {
        window.clearInterval(statsTimer);
        sender.close();
        stream.getTracks().forEach(track => track.stop());
      };
      let lastPreviewSent = -1;
      let previewPending = false;
      let frameInFlight = false;
      let encoder: VideoEncoder | undefined;
      let lastKeyframe = 0;
      let lastPreview = 0;
      let lastJpegAt = 0;
      // The KVM's processor is full at about 7.5 Mbit/s of camera frames,
      // and the mouse and keyboard then lag. Whatever rate the USB host
      // asked for, send at most 15 frames and MJPEG_BYTES_PER_SECOND a
      // second; the KVM repeats the last frame to the host in between.
      const fast = sender.transport === "websocket";
      const bytesPerSecond = fast ? MJPEG_FAST_BYTES_PER_SECOND : MJPEG_BYTES_PER_SECOND;
      let byteBudget = bytesPerSecond;
      let budgetAt = performance.now();
      // Draws the camera picture to fill the canvas, cropping what does not
      // fit, so a 4:3 source is not stretched into a 16:9 frame.
      const drawCover = () => {
        const sw = video.videoWidth || canvas.width;
        const sh = video.videoHeight || canvas.height;
        const scale = Math.max(canvas.width / sw, canvas.height / sh);
        const dw = sw * scale;
        const dh = sh * scale;
        ctx.drawImage(video, (canvas.width - dw) / 2, (canvas.height - dh) / 2, dw, dh);
      };
      const previewFrames = new Map<number, Promise<Uint8Array>>();
      const fail = (error: unknown) => {
        if (session !== sessionRef.current) return;
        ++sessionRef.current;
        cleanupRef.current?.();
        cleanupRef.current = null;
        setRunning(false);
        stopRemote();
        notifications.error(error instanceof Error ? error.message : "Camera connection lost");
      };
      if (nextConfig.codec === "h264") {
        encoder = new VideoEncoder({
          error: fail,
          output: chunk => {
            if (session !== sessionRef.current) return;
            // A data channel message is limited to 256 KiB. A larger access
            // unit is skipped, and the next one is a keyframe.
            if (chunk.byteLength > 240 * 1024) {
              lastKeyframe = 0;
              return;
            }
            const payload = new Uint8Array(chunk.byteLength);
            chunk.copyTo(payload);
            const seq = sequence++;
            sentFrames++;
            sentBytes += payload.byteLength;
            void sender
              .send(
                encodeFrame(
                  seq,
                  nextConfig.width,
                  nextConfig.height,
                  payload,
                  chunk.type === "key" ? 3 : 1,
                ),
              )
              .catch(fail);
            const preview = previewFrames.get(chunk.timestamp);
            previewFrames.delete(chunk.timestamp);
            if (preview)
              void preview
                .then(jpeg => {
                  if (
                    session === sessionRef.current &&
                    seq > lastPreviewSent &&
                    sequence - seq <= 60 &&
                    performance.now() - chunk.timestamp / 1000 < 4000 &&
                    sender.canSend()
                  ) {
                    lastPreviewSent = seq;
                    return sender.send(
                      encodeFrame(seq, nextConfig.width, nextConfig.height, jpeg, 4),
                    );
                  }
                })
                .catch(fail);
          },
        });
        cleanupRef.current = () => {
          window.clearInterval(statsTimer);
          if (encoder && encoder.state !== "closed") encoder.close();
          previewFrames.clear();
          sender.close();
          video.srcObject = null;
          stream.getTracks().forEach(track => track.stop());
        };
        encoder.configure(h264Config);
      }
      const interval = window.setInterval(
        async () => {
          if (frameInFlight || session !== sessionRef.current) return;
          if (encoder && (!sender.canSend() || encoder.encodeQueueSize > 1)) return;
          frameInFlight = true;
          try {
            if (!encoder) {
              // The USB host picks the frame size and rate; the KVM reports
              // them in its state and drops frames of another size.
              const live = stateRef.current;
              const width = live?.width || nextConfig.width;
              const height = live?.height || nextConfig.height;
              // Up to 15 frames a second, or the camera's set rate (up to
              // 30) when it is set above 15 and the frames go over the
              // WebSocket.
              const fps = Math.min(
                live?.fps || nextConfig.fps,
                fast && nextConfig.usb_fps > 15 ? nextConfig.usb_fps : 15,
              );
              const now = performance.now();
              byteBudget = Math.min(
                bytesPerSecond,
                byteBudget + ((now - budgetAt) / 1000) * bytesPerSecond,
              );
              budgetAt = now;
              if (byteBudget <= 0 || now - lastJpegAt < 1000 / fps - 4) return;
              lastJpegAt = now;
              if (canvas.width !== width || canvas.height !== height) {
                canvas.width = width;
                canvas.height = height;
              }
            }
            drawCover();
            if (encoder) {
              const now = performance.now();
              const timestamp = Math.round(now * 1000);
              // Independent IDRs every second allow USB host startup and recovery.
              const keyFrame =
                lastKeyframe === 0 ||
                now - lastKeyframe >= 1000 ||
                (!!stateRef.current?.needs_keyframe && now - lastKeyframe >= 250);
              if (keyFrame) lastKeyframe = now;
              if (!previewPending && previewFrames.size < 2 && now - lastPreview >= 1000) {
                lastPreview = now;
                previewPending = true;
                const preview = canvasToJpeg(canvas).finally(() => {
                  previewPending = false;
                });
                void preview.catch(() => {});
                previewFrames.set(timestamp, preview);
              }
              const frame = new VideoFrame(canvas, { timestamp });
              try {
                encoder.encode(frame, { keyFrame });
              } finally {
                frame.close();
              }
            } else {
              const jpeg = await canvasToJpeg(canvas);
              if (session !== sessionRef.current) return;
              // A data channel message is limited to 256 KiB: skip a frame
              // that compressed worse than that.
              if (!fast && jpeg.length > 240 * 1024) return;
              byteBudget -= jpeg.length;
              sentFrames++;
              sentBytes += jpeg.length;
              await sender.send(encodeFrame(sequence++, canvas.width, canvas.height, jpeg));
            }
          } catch (error) {
            fail(error);
          } finally {
            frameInFlight = false;
          }
        },
        nextConfig.codec === "h264" ? Math.max(1000 / nextConfig.fps, 1) : 1000 / 30,
      );
      cleanupRef.current = () => {
        window.clearInterval(statsTimer);
        window.clearInterval(interval);
        if (encoder && encoder.state !== "closed") encoder.close();
        previewFrames.clear();
        sender.close();
        video.srcObject = null;
        stream.getTracks().forEach(track => track.stop());
      };
      setRunning(true);
      notifications.success(
        `${nextConfig.codec.toUpperCase()} camera started using ${sender.transport}`,
      );
    } catch (error) {
      if (session !== sessionRef.current) return;
      cleanupRef.current?.();
      cleanupRef.current = null;
      stopRemote();
      notifications.error(
        error instanceof Error ? error.message : "Failed to start virtual camera",
      );
    } finally {
      if (session === sessionRef.current) {
        startingRef.current = false;
        setBusy(false);
      }
    }
  }, [
    config,
    sourceId,
    refreshSources,
    isKvmCamera,
    request,
    createWebSocketSender,
    createRTCChannel,
    sendConfig,
    startRemote,
    stopRemote,
  ]);

  const stop = useCallback(() => {
    ++sessionRef.current;
    startingRef.current = false;
    setBusy(false);
    cleanupRef.current?.();
    cleanupRef.current = null;
    stopRemote();
    setRunning(false);
    notifications.success("Virtual camera stopped");
  }, [stopRemote]);

  useEffect(() => {
    if (!running || state?.enabled !== false) return;
    ++sessionRef.current;
    cleanupRef.current?.();
    cleanupRef.current = null;
    setRunning(false);
  }, [running, state?.enabled]);

  const releaseCapture = useCallback(() => {
    ++sessionRef.current;
    startingRef.current = false;
    setBusy(false);
    cleanupRef.current?.();
    cleanupRef.current = null;
    setRunning(false);
  }, []);

  useEffect(() => releaseCapture, [peerConnection, releaseCapture]);

  useEffect(() => {
    if (!running) setStats(null);
  }, [running]);

  return {
    config,
    setConfig,
    state,
    running,
    busy,
    stats,
    usbFps,
    start,
    stop,
    syncState,
    watch,
    setUsbFormat,
    sourceId,
    setSourceId,
    sources,
    refreshSources,
  };
}

const CameraContext = createContext<ReturnType<typeof useCameraController> | null>(null);

export function VirtualCameraProvider({ children }: PropsWithChildren) {
  const controller = useCameraController();
  return <CameraContext.Provider value={controller}>{children}</CameraContext.Provider>;
}

// Call from a component that shows live camera state while it is mounted.
export function useCameraWatch() {
  const { watch } = useVirtualCamera();
  useEffect(() => watch(), [watch]);
}

export function useVirtualCamera() {
  const context = useContext(CameraContext);
  if (!context) throw new Error("Virtual camera controls require the device provider");
  return context;
}
