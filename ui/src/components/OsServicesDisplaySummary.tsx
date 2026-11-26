import { useEffect, useState } from "react";
import { LuArrowRight, LuMonitor } from "react-icons/lu";

import { LinkButton } from "@components/Button";
import { SettingsSectionHeader } from "@components/SettingsSectionHeader";
import { OsServicesSettings, useCapability, useVideoStore } from "@hooks/stores";
import { useDeviceUiNavigation } from "@hooks/useAppNavigation";
import { JsonRpcResponse, useJsonRpc } from "@hooks/useJsonRpc";
import { m } from "@localizations/messages.js";
import { cx } from "@/cva.config";

const formatResolution = (value: string) => value.replace("x", " × ");

// The target's virtual display (OS Services video) on the Display page: a
// read-only summary with a link to the OS Services page. Shown once OS
// Services is installed on the target (a sender connected now or before)
// and its video is available or turned on.
export default function OsServicesDisplaySummary() {
  const hasOsServices = useCapability("os_services");
  const { send } = useJsonRpc();
  const { getPath } = useDeviceUiNavigation();
  const { osServices, osServicesSettings, videoSource, hdmiState, width, height } = useVideoStore();
  const [loaded, setLoaded] = useState<OsServicesSettings | null>(null);

  useEffect(() => {
    if (!hasOsServices) return;
    void send("getOsServicesSettings", {}, (response: JsonRpcResponse) => {
      if (!("error" in response)) setLoaded(response.result as OsServicesSettings);
    });
  }, [hasOsServices, send]);

  // A settings announcement is newer than the reply.
  const settings = osServicesSettings ?? loaded;
  if (!hasOsServices || !settings || !settings.enabled) return null;

  const installed = !!osServices.sender || !!settings.last_sender_version;
  const videoAvailable = osServices.videoRegistered && !osServices.videoError;
  if (!installed || !(videoAvailable || settings.video_enabled)) return null;

  const shown = videoSource === "guest";
  const liveResolution =
    shown && hdmiState === "ready" && width > 0 && height > 0 ? `${width}x${height}` : null;

  const senderText = osServices.connected
    ? m.display_os_services_connected({ version: osServices.sender?.version ?? "" })
    : osServices.approvalPending
      ? m.display_os_services_pending()
      : m.display_os_services_away({ version: settings.last_sender_version ?? "" });

  const videoText = !settings.video_enabled
    ? m.display_os_services_video_off()
    : osServices.videoError
      ? m.display_os_services_video_faulted({ error: osServices.videoError })
      : osServices.connected && !osServices.videoRegistered
        ? m.display_os_services_video_unregistered()
        : shown
          ? m.display_os_services_video_shown()
          : m.display_os_services_video_ready();
  const videoTone = !settings.video_enabled
    ? "grey"
    : osServices.videoError || (osServices.connected && !osServices.videoRegistered)
      ? "amber"
      : shown
        ? "green"
        : "blue";

  const rows: { label: string; value: string; tone?: string }[] = [
    {
      label: m.display_os_services_sender(),
      value: senderText,
      tone: osServices.connected ? "green" : osServices.approvalPending ? "amber" : "grey",
    },
    { label: m.display_os_services_video(), value: videoText, tone: videoTone },
    {
      label: m.display_os_services_resolution(),
      value:
        liveResolution && liveResolution !== settings.resolution
          ? m.display_os_services_resolution_live({ resolution: formatResolution(liveResolution) })
          : formatResolution(settings.resolution),
    },
    {
      label: m.display_os_services_mode(),
      value:
        settings.display_mode === "mirror"
          ? m.os_services_display_mode_mirror()
          : m.os_services_display_mode_extend(),
    },
    {
      label: m.display_os_services_capture(),
      value: settings.capture_display || m.os_services_capture_auto(),
    },
  ];

  return (
    <div className="space-y-4">
      <div className="h-px w-full bg-slate-800/10 dark:bg-slate-300/20" />
      <SettingsSectionHeader
        title={m.display_os_services_title()}
        description={m.display_os_services_description()}
      />
      <div className="rounded-md border border-slate-800/10 p-3 dark:border-slate-300/20">
        <div className="flex items-start gap-3">
          <LuMonitor className="mt-0.5 h-5 w-5 shrink-0 text-slate-500 dark:text-slate-400" />
          <dl className="grid min-w-0 flex-1 grid-cols-[auto_1fr] gap-x-4 gap-y-1.5 text-sm">
            {rows.map(row => (
              <div key={row.label} className="contents">
                <dt className="text-slate-600 dark:text-slate-400">{row.label}</dt>
                <dd className="flex min-w-0 items-center gap-1.5 text-slate-900 dark:text-white">
                  {row.tone && (
                    <span
                      className={cx("h-2 w-2 shrink-0 rounded-full", {
                        "bg-green-500": row.tone === "green",
                        "bg-blue-500": row.tone === "blue",
                        "bg-amber-500": row.tone === "amber",
                        "bg-slate-400": row.tone === "grey",
                      })}
                    />
                  )}
                  <span className="truncate" title={row.value}>
                    {row.value}
                  </span>
                </dd>
              </div>
            ))}
          </dl>
        </div>
        <div className="mt-3 flex justify-end">
          <LinkButton
            to={getPath("/settings/os-services")}
            size="SM"
            theme="light"
            text={m.display_os_services_open()}
            TrailingIcon={LuArrowRight}
          />
        </div>
      </div>
    </div>
  );
}
