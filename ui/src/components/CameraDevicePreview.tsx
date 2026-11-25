import { useEffect, useId, useRef, useState } from "react";
import { useDeviceRpc } from "@hooks/useDeviceRpc";
import { useCameraWatch, useVirtualCamera } from "@hooks/VirtualCameraProvider";
import { SelectMenuBasic } from "@components/SelectMenuBasic";

interface Preview {
  jpeg?: string;
  frame_at?: string;
}

// The frame sizes and rates the MJPEG USB camera can offer
// (internal/usbgadget/video.go), smallest first.
export const usbFrameSizes = [
  { value: "360", label: "640×360" },
  { value: "480", label: "640×480" },
  { value: "540", label: "960×540" },
  { value: "600", label: "800×600" },
  { value: "720", label: "1280×720" },
  { value: "768", label: "1024×768" },
  { value: "900", label: "1600×900" },
  { value: "1080", label: "1920×1080" },
];
export const usbFrameRates = [5, 10, 15, 20, 24, 25, 30];

export function CameraCodecSelect() {
  const id = useId();
  const { config, setConfig, running, busy } = useVirtualCamera();
  return (
    <div className="space-y-1">
      <label className="text-sm font-medium" htmlFor={id}>
        Camera output
      </label>
      <SelectMenuBasic
        id={id}
        size="SM"
        value={config.codec}
        disabled={running || busy}
        onChange={event => {
          setConfig(previous => ({
            ...previous,
            output: "usb",
            codec: event.target.value === "h264" ? "h264" : "mjpeg",
            transport: "auto",
            width: 640,
            height: 480,
            fps: 15,
          }));
        }}
        options={[
          { value: "mjpeg", label: "USB camera · MJPEG · compatible" },
          { value: "h264", label: "USB camera · H.264 · experimental" },
        ]}
      />
      {config.codec === "h264" && (
        <p className="text-xs text-amber-700 dark:text-amber-300">
          Requires H.264 support in the target app. Choose MJPEG if it cannot open the camera.
          Changing format reconnects the USB devices.
        </p>
      )}
    </div>
  );
}

// The frame size and rate of the MJPEG USB camera. With one size or rate the
// camera offers nothing else, so the target computer cannot pick another.
export function CameraFormatSelect() {
  const sizeId = useId();
  const rateId = useId();
  const { config, setUsbFormat, running, busy } = useVirtualCamera();
  if (config.codec !== "mjpeg") return null;
  return (
    <div className="space-y-1">
      <div className="grid grid-cols-2 gap-3">
        <div className="space-y-1">
          <label className="text-sm font-medium" htmlFor={sizeId}>
            Frame size
          </label>
          <SelectMenuBasic
            id={sizeId}
            size="SM"
            value={config.usb_size}
            disabled={running || busy}
            onChange={event => void setUsbFormat({ usb_size: event.target.value })}
            options={[{ value: "", label: "Target picks" }, ...usbFrameSizes]}
          />
        </div>
        <div className="space-y-1">
          <label className="text-sm font-medium" htmlFor={rateId}>
            Frame rate
          </label>
          <SelectMenuBasic
            id={rateId}
            size="SM"
            value={String(config.usb_fps)}
            disabled={running || busy}
            onChange={event => void setUsbFormat({ usb_fps: Number(event.target.value) })}
            options={[
              { value: "0", label: "Target picks" },
              ...usbFrameRates.map(fps => ({ value: String(fps), label: `${fps} fps` })),
            ]}
          />
        </div>
      </div>
      <p className="text-xs text-slate-600 dark:text-slate-400">
        A change reconnects the KVM's USB devices on the target computer.
      </p>
    </div>
  );
}

export function CameraSourceSelect() {
  const id = useId();
  const { sources, sourceId, setSourceId, refreshSources, running } = useVirtualCamera();
  useEffect(() => {
    void refreshSources().catch(() => {});
  }, [refreshSources]);
  return (
    <div className="space-y-1">
      <label className="text-sm font-medium" htmlFor={id}>
        Browser camera source
      </label>
      <SelectMenuBasic
        id={id}
        size="SM"
        value={sourceId}
        disabled={running}
        onChange={event => setSourceId(event.target.value)}
        options={[
          { value: "", label: "Default laptop camera" },
          ...sources.map((source, index) => ({
            value: source.deviceId,
            label: source.label || `Camera ${index + 1}`,
          })),
        ]}
      />
    </div>
  );
}

export function CameraDevicePreview() {
  useCameraWatch();
  const { state } = useVirtualCamera();
  const { request, ready } = useDeviceRpc();
  const [preview, setPreview] = useState<Preview | null>(null);
  const [error, setError] = useState("");
  const [now, setNow] = useState(Date.now());
  const lastFrame = useRef({ timestamp: "", receivedAt: 0 });
  useEffect(() => {
    setPreview(null);
    lastFrame.current = { timestamp: "", receivedAt: 0 };
    setError("");
    if (!ready || !state?.enabled) return;
    let cancelled = false,
      pending = false;
    const refresh = async () => {
      if (pending) return;
      pending = true;
      try {
        const frame = await request<Preview>("getVirtualVideoPreview");
        if (!cancelled) {
          if (frame.jpeg && frame.frame_at && frame.frame_at !== lastFrame.current.timestamp)
            lastFrame.current = {
              timestamp: frame.frame_at,
              receivedAt: Date.now(),
            };
          setPreview(frame);
          setError("");
        }
      } catch (failure) {
        if (!cancelled) {
          setPreview(null);
          setError(failure instanceof Error ? failure.message : "Preview unavailable");
        }
      } finally {
        pending = false;
      }
    };
    void refresh();
    const timer = window.setInterval(() => {
      setNow(Date.now());
      void refresh();
    }, 1000);
    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, [request, ready, state?.enabled]);
  const fresh = state?.enabled && !!preview?.frame_at && now - lastFrame.current.receivedAt < 4000;
  return (
    <div className="space-y-1">
      <div className="relative flex aspect-[4/3] items-center justify-center overflow-hidden rounded bg-slate-950 text-slate-300">
        {preview?.jpeg && fresh ? (
          <img
            className="h-full w-full object-contain"
            src={`data:image/jpeg;base64,${preview.jpeg}`}
            alt="Camera frame received by JetKVM for USB output"
          />
        ) : (
          <p className="px-4 text-center text-sm">
            {error || (state?.enabled ? "Waiting for camera footage" : "Camera sending is stopped")}
          </p>
        )}
      </div>
      <p className="text-xs text-slate-500">
        {state?.codec === "h264"
          ? "Sampled source preview returned by JetKVM."
          : "Live preview returned by JetKVM."}{" "}
        {state?.usb_streaming
          ? "The target is reading USB video."
          : "The target has not opened the USB camera."}
      </p>
    </div>
  );
}
