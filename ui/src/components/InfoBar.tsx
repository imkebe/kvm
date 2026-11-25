import { useEffect, useMemo, useState } from "react";
import { LuCheck, LuCircleX, LuUpload, LuX } from "react-icons/lu";

import {
  useCapability,
  useHidStore,
  useMouseStore,
  useRTCStore,
  useSettingsStore,
  useVideoStore,
  VideoState,
} from "@hooks/stores";
import { useGuestFileTransferStore } from "@hooks/useGuestFileDrop";
import { useHidRpc } from "@hooks/useHidRpc";
import useKeyboard from "@hooks/useKeyboard";
import { keys, modifiers } from "@/keyboardMappings";
import { cx } from "@/cva.config";
import { m } from "@localizations/messages.js";

// One cell of the OS Services state: dim until its state is on.
function GuestIndicator({ on, label }: { on: boolean; label: string }) {
  return (
    <div
      className={cx(
        "shrink-0 p-1 px-1.5 text-xs",
        on ? "text-black dark:text-white" : "text-slate-800/20 dark:text-slate-300/20",
      )}
    >
      {label}
    </div>
  );
}

// Files on their way to the target (a drop on the video): the same drop as
// the status card, shown and cleared with it. Fixed widths, so the bar does
// not move while the percent changes.
function GuestFileTransferIndicator() {
  const { visible, transfers, cancel } = useGuestFileTransferStore();
  if (!visible || transfers.length === 0) return null;
  const sending = cancel !== null;
  const failed = transfers.some(t => t.status === "failed" || t.status === "cancelled");
  const sent = transfers.reduce((total, t) => total + t.sent, 0);
  const size = transfers.reduce((total, t) => total + t.size, 0);
  const percent = size === 0 ? 100 : Math.min(100, Math.round((sent / size) * 100));
  const subject =
    transfers.length === 1
      ? transfers[0].name
      : m.guest_file_transfer_count({ count: transfers.length });
  const title = sending
    ? m.info_file_transfer_sending({ subject, percent })
    : failed
      ? m.info_file_transfer_failed({ subject })
      : m.guest_file_transfer_done();
  return (
    <div className="flex shrink-0 items-center gap-x-1.5 p-1 px-1.5 text-xs" title={title}>
      {sending ? (
        <LuUpload className="h-3.5 w-3.5 shrink-0 text-blue-600 dark:text-blue-400" />
      ) : failed ? (
        <LuCircleX className="h-3.5 w-3.5 shrink-0 text-red-600 dark:text-red-400" />
      ) : (
        <LuCheck className="h-3.5 w-3.5 shrink-0 text-emerald-600 dark:text-emerald-400" />
      )}
      <span className="max-w-32 truncate">{subject}</span>
      <div className="h-1.5 w-16 overflow-hidden rounded-full bg-slate-500/20">
        <div
          className={cx("h-full rounded-full transition-[width] duration-200", {
            "bg-blue-500": sending,
            "bg-red-500": !sending && failed,
            "bg-emerald-500": !sending && !failed,
          })}
          style={{ width: `${percent}%` }}
        />
      </div>
      <span className="w-8 text-right tabular-nums">{percent}%</span>
      {sending && (
        <button
          type="button"
          onClick={() => cancel?.()}
          aria-label={m.info_file_transfer_cancel()}
          title={m.info_file_transfer_cancel()}
          className="rounded p-0.5 text-slate-500 hover:bg-slate-200 hover:text-slate-800 dark:text-slate-400 dark:hover:bg-slate-700 dark:hover:text-slate-200"
        >
          <LuX className="h-3 w-3" />
        </button>
      )}
    </div>
  );
}

export default function InfoBar() {
  const { keysDownState } = useHidStore();
  const { mouseX, mouseY, mouseMove } = useMouseStore();
  const { rpcHidStatus, rpcHidReady } = useHidRpc();
  const { handleKeyPress } = useKeyboard();

  const videoClientSize = useVideoStore(
    (state: VideoState) => `${Math.round(state.clientWidth)}x${Math.round(state.clientHeight)}`,
  );

  const videoSize = useVideoStore(
    (state: VideoState) => `${Math.round(state.width)}x${Math.round(state.height)}`,
  );

  const { debugMode, mouseMode, showPressedKeys } = useSettingsStore();
  const { isPasteInProgress } = useHidStore();
  const { keyboardLedState, usbState } = useHidStore();
  const { isTurnServerInUse, peerConnection, peerConnectionState, rpcDataChannel } = useRTCStore();
  const { hdmiState, osServices } = useVideoStore();
  const hasOsServices = useCapability("os_services");

  const keyboardAvailable =
    usbState === "configured" && (rpcHidReady || rpcDataChannel?.readyState === "open");

  const toggleLock = (key: number) => {
    if (!keyboardAvailable) return;
    void handleKeyPress(key, true);
    setTimeout(() => void handleKeyPress(key, false), 100);
  };

  const [videoCodec, setVideoCodec] = useState<string | null>(null);
  const [videoBitrate, setVideoBitrate] = useState<string | null>(null);

  useEffect(() => {
    if (!debugMode || !peerConnection || peerConnectionState !== "connected") {
      setVideoCodec(null);
      setVideoBitrate(null);
      return;
    }

    let cancelled = false;
    let lastBytes = 0;
    let lastTimestamp = 0;

    async function extractStats() {
      const stats = await peerConnection!.getStats();
      let codecId: string | null = null;
      stats.forEach(report => {
        if (report.type === "inbound-rtp" && report.kind === "video") {
          codecId = report.codecId ?? null;

          const bytes = report.bytesReceived as number;
          const ts = report.timestamp as number;
          if (lastBytes > 0 && lastTimestamp > 0) {
            const deltaSec = (ts - lastTimestamp) / 1000;
            if (deltaSec > 0) {
              const kbps = ((bytes - lastBytes) * 8) / 1000 / deltaSec;
              setVideoBitrate(`${Math.round(kbps)} kbps`);
            }
          }
          lastBytes = bytes;
          lastTimestamp = ts;
        }
      });
      if (cancelled || !codecId) return false;
      const codecReport = stats.get(codecId);
      if (codecReport?.mimeType) {
        const mime = codecReport.mimeType as string;
        const short = mime.replace("video/", "").replace("H264", "H.264").replace("H265", "H.265");
        setVideoCodec(short);
        return true;
      }
      return false;
    }

    // Stats may not be available immediately — retry a few times for codec,
    // then keep polling for bitrate updates.
    let codecResolved = false;
    let attempts = 0;
    const timer = setInterval(async () => {
      if (cancelled) {
        clearInterval(timer);
        return;
      }
      const found = await extractStats();
      if (found && !codecResolved) codecResolved = true;
      if (!codecResolved && attempts++ >= 10) {
        clearInterval(timer);
      }
    }, 1000);

    return () => {
      cancelled = true;
      clearInterval(timer);
    };
  }, [debugMode, peerConnection, peerConnectionState]);

  const displayKeys = useMemo(() => {
    if (!showPressedKeys) return "";

    const activeModifierMask = keysDownState.modifier || 0;
    const keysDown = keysDownState.keys || [];
    const modifierNames = Object.entries(modifiers)
      .filter(([_, mask]) => (activeModifierMask & mask) !== 0)
      .map(([name]) => name);
    const keyNames = Object.entries(keys)
      .filter(([_, value]) => keysDown.includes(value))
      .map(([name]) => name);

    return [...modifierNames, ...keyNames].join(", ");
  }, [keysDownState, showPressedKeys]);

  return (
    <div className="border-t border-t-slate-800/30 bg-white text-slate-800 dark:border-t-slate-300/20 dark:bg-slate-900 dark:text-slate-300">
      <div className="flex flex-wrap items-stretch justify-between gap-1">
        <div className="flex items-center">
          <div className="flex flex-wrap items-center gap-x-4 pl-2">
            {debugMode ? (
              <div className="flex">
                <span className="text-xs font-semibold">{m.info_resolution()}</span>{" "}
                <span className="text-xs">{videoSize}</span>
              </div>
            ) : null}

            {debugMode ? (
              <div className="flex">
                <span className="text-xs font-semibold">{m.info_video_size()}</span>
                <span className="text-xs">{videoClientSize}</span>
              </div>
            ) : null}

            {debugMode && mouseMode == "absolute" ? (
              <div className="flex w-[118px] items-center gap-x-1">
                <span className="text-xs font-semibold">{m.info_pointer()}</span>
                <span className="text-xs">
                  {mouseX},{mouseY}
                </span>
              </div>
            ) : null}

            {debugMode && mouseMode == "relative" ? (
              <div className="flex w-[118px] items-center gap-x-1">
                <span className="text-xs font-semibold">{m.info_last_move()}</span>
                <span className="text-xs">
                  {mouseMove
                    ? `${mouseMove.x},${mouseMove.y} ${mouseMove.buttons ? `(${mouseMove.buttons})` : ""}`
                    : "N/A"}
                </span>
              </div>
            ) : null}

            {debugMode && (
              <div className="flex w-[156px] items-center gap-x-1">
                <span className="text-xs font-semibold">{m.info_usb_state()}</span>
                <span className="text-xs">{usbState}</span>
              </div>
            )}

            {debugMode && (
              <div className="flex w-[156px] items-center gap-x-1">
                <span className="text-xs font-semibold">{m.info_hdmi_state()}</span>
                <span className="text-xs">{hdmiState}</span>
              </div>
            )}

            {debugMode && videoCodec && (
              <div className="flex items-center gap-x-1">
                <span className="text-xs font-semibold">{m.info_codec()}</span>
                <span className="text-xs">{videoCodec}</span>
              </div>
            )}

            {debugMode && videoBitrate && (
              <div className="flex items-center gap-x-1">
                <span className="text-xs font-semibold">{m.info_bitrate()}</span>
                <span className="text-xs">{videoBitrate}</span>
              </div>
            )}

            {debugMode && (
              <div className="flex w-[156px] items-center gap-x-1">
                <span className="text-xs font-semibold">{m.info_hidrpc_state()}</span>
                <span className="text-xs">{rpcHidStatus}</span>
              </div>
            )}

            {isPasteInProgress && (
              <div className="flex w-[156px] items-center gap-x-1">
                <span className="text-xs font-semibold">{m.info_paste_mode()}</span>
                <span className="text-xs">{m.info_paste_enabled()}</span>
              </div>
            )}

            {showPressedKeys && (
              <div className="flex items-center gap-x-1">
                <span className="text-xs font-semibold">{m.info_keys()}</span>
                <h2 className="text-xs">{displayKeys}</h2>
              </div>
            )}
          </div>
        </div>

        <div className="first:divide-l flex items-center divide-x divide-slate-800/20 dark:divide-slate-300/20">
          {isTurnServerInUse && (
            <div className="shrink-0 p-1 px-1.5 text-xs text-black dark:text-white">
              {m.info_relayed_by_cloudflare()}
            </div>
          )}

          <GuestFileTransferIndicator />

          {hasOsServices && osServices.enabled && (
            <div className="flex items-center divide-x divide-slate-800/20 border-r-2 border-r-slate-800/30 dark:divide-slate-300/20 dark:border-r-slate-300/30">
              <GuestIndicator on={osServices.connected} label={m.info_os_services()} />
              <GuestIndicator on={osServices.video} label={m.info_guest_video()} />
              <GuestIndicator on={osServices.clipboard} label={m.info_guest_clipboard()} />
            </div>
          )}

          <LockToggle
            label={m.info_caps_lock()}
            active={keyboardLedState.caps_lock}
            disabled={!keyboardAvailable}
            onToggle={() => toggleLock(keys.CapsLock)}
          />

          <LockToggle
            label={m.info_num_lock()}
            active={keyboardLedState.num_lock}
            disabled={!keyboardAvailable}
            onToggle={() => toggleLock(keys.NumLock)}
          />

          <LockToggle
            label={m.info_scroll_lock()}
            active={keyboardLedState.scroll_lock}
            disabled={!keyboardAvailable}
            onToggle={() => toggleLock(keys.ScrollLock)}
          />

          {keyboardLedState.compose ? (
            <div className="shrink-0 p-1 px-1.5 text-xs">{m.info_compose()}</div>
          ) : null}

          {keyboardLedState.kana ? (
            <div className="shrink-0 p-1 px-1.5 text-xs">{m.info_kana()}</div>
          ) : null}

          {keyboardLedState.shift ? (
            <div className="shrink-0 p-1 px-1.5 text-xs">{m.info_shift()}</div>
          ) : null}
        </div>
      </div>
    </div>
  );
}

function LockToggle({
  label,
  active,
  disabled,
  onToggle,
}: {
  label: string;
  active: boolean;
  disabled: boolean;
  onToggle: () => void;
}) {
  return (
    <button
      type="button"
      aria-pressed={active}
      disabled={disabled}
      onMouseDown={e => e.preventDefault()}
      onClick={onToggle}
      className={cx(
        "shrink-0 cursor-pointer p-1 px-1.5 text-xs outline-hidden disabled:cursor-default",
        "focus-visible:ring-1 focus-visible:ring-blue-700 focus-visible:ring-inset enabled:hover:bg-slate-800/5 dark:enabled:hover:bg-slate-300/10",
        active ? "text-black dark:text-white" : "text-slate-800/20 dark:text-slate-300/20",
      )}
    >
      {label}
    </button>
  );
}
