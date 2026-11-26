import { Button } from "@components/Button";
import Fieldset from "@components/Fieldset";
import { SelectMenuBasic } from "@components/SelectMenuBasic";
import { SettingsItem } from "@components/SettingsItem";
import { SettingsSectionHeader } from "@components/SettingsSectionHeader";

import {
  useCameraWatch,
  useVirtualCamera,
  type VirtualVideoTransport,
} from "@hooks/VirtualCameraProvider";
import {
  CameraDevicePreview,
  CameraSourceSelect,
  CameraFormatSelect,
} from "@components/CameraDevicePreview";

export function VirtualVideoSetting() {
  useCameraWatch();
  const { config, setConfig, state, running, busy, start, stop, syncState } = useVirtualCamera();
  return (
    <Fieldset disabled={busy} className="space-y-4">
      <div className="h-px w-full bg-slate-800/10 dark:bg-slate-300/20" />
      <SettingsSectionHeader
        title="Virtual Camera"
        description="Stream this browser's camera to the target as a USB video device"
      />
      {!window.isSecureContext && (
        <p className="text-sm text-amber-700 dark:text-amber-300">
          Camera access requires HTTPS.{" "}
          <a
            className="underline"
            href={`https://${window.location.hostname}${window.location.pathname}`}
          >
            Open secure camera settings
          </a>{" "}
          after enabling HTTPS in Access settings.
        </p>
      )}
      <CameraSourceSelect />
      <SettingsItem
        title="Transport"
        description="H.264 uses ordered WebRTC; MJPEG automatic uses the WebSocket and falls back to WebRTC"
      >
        <SelectMenuBasic
          size="SM"
          disabled={running || config.codec === "h264"}
          value={config.codec === "h264" ? "rtc-datachannel" : config.transport}
          onChange={e =>
            setConfig(prev => ({
              ...prev,
              transport: e.target.value as VirtualVideoTransport,
            }))
          }
          options={[
            { value: "auto", label: "Automatic" },
            {
              value: "websocket",
              label: "WebSocket · needed for 720p and 1080p",
            },
            { value: "rtc-datachannel", label: "WebRTC data channel" },
          ]}
        />
      </SettingsItem>
      <CameraFormatSelect />
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
      <CameraDevicePreview />
      {state && (
        <p className="text-xs text-slate-600 dark:text-slate-300">
          Frames received: {state.frames_written}. USB frames sent: {state.usb_frames_sent ?? 0}.
          {state.usb_streaming
            ? " Target camera is active."
            : " Waiting for the target to open the camera."}
        </p>
      )}
      {state?.last_error && (
        <p className="text-xs text-red-700 dark:text-red-300">{state.last_error}</p>
      )}
    </Fieldset>
  );
}
