import { useState } from "react";
import { Menu, MenuButton, MenuItem, MenuItems } from "@headlessui/react";
import { LuChevronDown } from "react-icons/lu";

import { cx } from "@/cva.config";
import notifications from "@/notifications";
import { useCapability, useVideoStore } from "@hooks/stores";
import { useDeviceRpc } from "@hooks/useDeviceRpc";
import StatusCard from "@components/StatusCards";
import { m } from "@localizations/messages.js";

const green = "bg-green-500 border-green-600";
const yellow = "bg-yellow-400 border-yellow-500";
const red = "bg-red-500 border-red-600";
const grey = "bg-slate-300 border-slate-400";

type Choice = "hdmi" | "usb";

interface Entry {
  choice: Choice;
  name: string;
  // Green: shown now; yellow: ready to show; red: shown but failing; grey:
  // not available.
  dot: string;
  detail: string;
  available: boolean;
}

// The third card of the header: which video the KVM shows, HDMI from the
// target's graphics output or USB Video from IP-KVM OS Services on the
// target, and a menu that switches between them. The switch is the OS
// Services "Video streaming" setting: on, the KVM shows the sender's video
// while a sender with video is connected; off, it shows HDMI.
export default function VideoSourceStatus() {
  const hasOsServices = useCapability("os_services");
  const { videoSource, hdmiState, osServices, osServicesSettings } = useVideoStore();
  const { request, ready } = useDeviceRpc();
  const [busy, setBusy] = useState(false);

  const hdmiReady = hdmiState === "ready";
  const usbActive = videoSource === "guest";
  const switching = videoSource === "switching_to_guest" || videoSource === "switching_to_physical";
  // A connected sender that registered video, without a fault, can show it.
  const usbAvailable =
    osServices.enabled &&
    osServices.connected &&
    osServices.videoRegistered &&
    !osServices.videoError;

  let status: string;
  let dot: string;
  if (videoSource === "switching_to_guest") {
    status = m.video_source_switching_usb();
    dot = grey;
  } else if (videoSource === "switching_to_physical") {
    status = m.video_source_switching_hdmi();
    dot = grey;
  } else if (usbActive) {
    status = hdmiReady ? m.video_source_usb_connected() : m.video_source_usb_starting();
    dot = hdmiReady ? green : grey;
  } else if (hdmiReady) {
    status = m.video_source_hdmi_connected();
    dot = green;
  } else {
    status =
      hdmiState === "no_signal"
        ? m.video_source_hdmi_no_signal()
        : hdmiState === "no_lock" || hdmiState === "out_of_range"
          ? m.video_source_hdmi_signal_error()
          : m.video_source_hdmi_connecting();
    dot = hdmiState === "connecting" ? grey : red;
  }

  const usbReason = !osServices.enabled
    ? m.video_source_os_services_off()
    : osServices.approvalPending
      ? m.video_source_waiting_approval()
      : !osServices.connected
        ? m.video_source_os_services_not_connected()
        : osServices.videoError
          ? osServices.videoError
          : !osServices.videoRegistered
            ? m.video_source_no_capture()
            : usbActive
              ? m.video_source_showing()
              : osServicesSettings?.video_enabled
                ? m.video_source_ready()
                : m.video_source_ready_off();
  const entries: Entry[] = [
    {
      choice: "hdmi",
      name: m.video_source_hdmi(),
      dot: usbActive ? yellow : hdmiReady ? green : hdmiState === "connecting" ? grey : red,
      detail: usbActive ? m.video_source_standby() : hdmiReady ? m.video_source_showing() : status,
      available: true,
    },
    {
      choice: "usb",
      name: m.video_source_usb(),
      dot: usbActive ? (hdmiReady ? green : grey) : usbAvailable ? yellow : grey,
      detail: usbReason,
      available: usbAvailable || usbActive,
    },
  ];
  const current: Choice = usbActive || videoSource === "switching_to_guest" ? "usb" : "hdmi";

  const choose = (choice: Choice) => {
    if (choice === current || busy) return;
    setBusy(true);
    request("setOsServicesSettings", { settings: { video_enabled: choice === "usb" } })
      .catch((error: Error) =>
        notifications.error(m.video_source_switch_failed({ error: error.message })),
      )
      .finally(() => setBusy(false));
  };

  const action = hasOsServices ? (
    <Menu>
      <MenuButton
        title={m.video_source_choose()}
        aria-label={m.video_source_choose()}
        disabled={!ready || switching || busy}
        className="flex h-7 w-7 items-center justify-center rounded-md bg-blue-700 text-white shadow-sm transition-colors hover:bg-blue-800 focus:outline-none focus-visible:ring-2 focus-visible:ring-blue-400 disabled:opacity-50 data-open:bg-blue-900 dark:bg-blue-600 dark:hover:bg-blue-500"
      >
        <LuChevronDown className="h-4 w-4" />
      </MenuButton>
      <MenuItems
        anchor="bottom end"
        transition
        className="z-30 w-64 origin-top rounded-md border border-slate-800/20 bg-white p-1 shadow-lg [--anchor-gap:8px] focus:outline-none data-closed:opacity-0 dark:border-slate-600 dark:bg-slate-800 dark:text-white"
      >
        {entries.map(entry => (
          <MenuItem key={entry.choice} disabled={!entry.available}>
            <button
              type="button"
              onClick={() => choose(entry.choice)}
              className={cx(
                "flex w-full items-center gap-x-2 rounded px-2 py-1.5 text-left text-sm",
                "data-disabled:cursor-not-allowed data-disabled:opacity-50 data-focus:bg-slate-100 dark:data-focus:bg-slate-700",
                entry.choice === current && "font-semibold",
              )}
            >
              <span className={cx("h-2.5 w-2.5 shrink-0 rounded-full border", entry.dot)} />
              <span className="min-w-0 flex-1">
                <span className="block">{entry.name}</span>
                <span className="block truncate text-xs font-normal text-slate-500 dark:text-slate-400">
                  {entry.detail}
                </span>
              </span>
              {entry.choice === current && <span className="text-xs text-blue-600">✓</span>}
            </button>
          </MenuItem>
        ))}
      </MenuItems>
    </Menu>
  ) : undefined;

  return (
    <StatusCard
      title={m.video_source_title()}
      status={status}
      statusIndicatorClassName={dot}
      action={action}
    />
  );
}
