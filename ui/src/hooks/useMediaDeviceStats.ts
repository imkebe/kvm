import { useEffect, useRef, useState } from "react";

import { useVirtualCamera } from "@hooks/VirtualCameraProvider";
import {
  useVirtualMicrophone,
  type VirtualMicrophoneState,
} from "@hooks/VirtualMicrophoneProvider";
import { useDeviceRpc } from "@hooks/useDeviceRpc";

const MAX_POINTS = 120;

export interface CameraPoint {
  sentFps: number;
  usbFps: number;
  mbps: number;
  droppedPerSecond: number;
}

export interface MicrophonePoint {
  kiloSamplesPerSecond: number;
  droppedPerSecond: number;
  level: number;
}

function push<T>(map: Map<number, T>, key: number, value: T) {
  const next = new Map(map);
  next.set(key, value);
  while (next.size > MAX_POINTS) {
    const oldest = next.keys().next().value;
    if (oldest === undefined) break;
    next.delete(oldest);
  }
  return next;
}

// Keeps one-second history of the USB camera and USB microphone for the
// connection stats sidebar. Mount it only while the sidebar is open.
export function useMediaDeviceStats() {
  const camera = useVirtualCamera();
  const microphone = useVirtualMicrophone();
  const { request } = useDeviceRpc();

  const [cameraHistory, setCameraHistory] = useState<Map<number, CameraPoint>>(new Map());
  const [microphoneHistory, setMicrophoneHistory] = useState<Map<number, MicrophonePoint>>(
    new Map(),
  );
  const [microphoneState, setMicrophoneState] = useState<VirtualMicrophoneState | null>(null);

  const cameraActive = camera.running || camera.state?.enabled === true;
  const microphoneActive =
    microphone.running || microphoneState?.enabled === true || microphone.state?.enabled === true;

  // Latest values, read by the 1 s sampler without restarting it.
  const latest = useRef({ camera, microphone, microphoneState });
  latest.current = { camera, microphone, microphoneState };
  const prev = useRef<{ dropped?: number; written?: number; micDropped?: number }>({});

  // The microphone provider reads its state once at start; poll it here.
  useEffect(() => {
    if (!microphone.running) return;
    let cancelled = false;
    const poll = async () => {
      try {
        const next = await request<VirtualMicrophoneState>("getVirtualAudioState");
        if (!cancelled) setMicrophoneState(next);
      } catch {
        /* The device may be reconnecting; the next poll retries. */
      }
    };
    void poll();
    const timer = window.setInterval(poll, 1000);
    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, [microphone.running, request]);

  useEffect(() => {
    const timer = window.setInterval(() => {
      const { camera: cam, microphone: mic, microphoneState: micState } = latest.current;
      const date = Math.floor(Date.now() / 1000);

      if (cam.running || cam.state?.enabled) {
        const dropped = cam.state?.dropped_frames;
        const delta =
          dropped != null && prev.current.dropped != null
            ? Math.max(dropped - prev.current.dropped, 0)
            : 0;
        prev.current.dropped = dropped;
        const point: CameraPoint = {
          sentFps: cam.stats?.fps ?? 0,
          usbFps: cam.usbFps ?? 0,
          mbps: (cam.stats?.bitsPerSecond ?? 0) / 1e6,
          droppedPerSecond: delta,
        };
        setCameraHistory(h => push(h, date, point));
      } else {
        prev.current.dropped = undefined;
      }

      if (mic.running && micState) {
        const written =
          prev.current.written != null
            ? Math.max(micState.framesWritten - prev.current.written, 0)
            : 0;
        const dropped =
          prev.current.micDropped != null
            ? Math.max(micState.framesDropped - prev.current.micDropped, 0)
            : 0;
        prev.current.written = micState.framesWritten;
        prev.current.micDropped = micState.framesDropped;
        const point: MicrophonePoint = {
          kiloSamplesPerSecond: written / 1000,
          droppedPerSecond: dropped,
          level: Math.round(mic.level * 100),
        };
        setMicrophoneHistory(h => push(h, date, point));
      } else {
        prev.current.written = undefined;
        prev.current.micDropped = undefined;
      }
    }, 1000);
    return () => window.clearInterval(timer);
  }, []);

  return {
    cameraActive,
    microphoneActive,
    cameraHistory,
    microphoneHistory,
    microphoneState: microphoneState ?? microphone.state,
  };
}
