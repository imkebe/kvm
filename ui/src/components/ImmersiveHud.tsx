import { forwardRef, Fragment, useCallback, useEffect, useRef, useState } from "react";
import { Popover, PopoverButton, PopoverPanel } from "@headlessui/react";
import { FaKeyboard } from "react-icons/fa6";
import {
  LuCamera,
  LuChevronsUp,
  LuHardDrive,
  LuMaximize,
  LuMinimize,
  LuPanelTop,
  LuTerminal,
} from "react-icons/lu";
import { MdOutlineContentPasteGo } from "react-icons/md";

import { cx } from "@/cva.config";
import InfoBar from "@components/InfoBar";
import MountPopopover from "@components/popovers/MountPopover";
import PasteModal from "@components/popovers/PasteModal";
import {
  AvailableTerminalTypes,
  useCapability,
  useHidStore,
  useMountMediaStore,
  useSettingsStore,
  useUiStore,
  useVideoStore,
} from "@hooks/stores";
import { takeScreenshot } from "@hooks/useStreamCapture";
import notifications from "@/notifications";
import { m } from "@localizations/messages.js";

// How close to the top or bottom edge the pointer must be, with Shift held,
// to bring the HUD in.
const EDGE_PX = 48;

const HudIcon = forwardRef<
  HTMLButtonElement,
  {
    label: string;
    icon: React.ComponentType<{ className?: string }>;
    active?: boolean;
    // A dot in the corner: something keeps running behind this button.
    badge?: boolean;
  } & React.ButtonHTMLAttributes<HTMLButtonElement>
>(function HudIcon({ label, icon: Icon, active, badge, className, ...props }, ref) {
  return (
    <button
      ref={ref}
      type="button"
      title={label}
      aria-label={label}
      {...props}
      className={cx(
        "relative flex h-9 w-9 items-center justify-center rounded-md text-slate-200 transition-colors hover:bg-white/15 hover:text-white",
        active && "bg-white/20 text-white",
        className,
      )}
    >
      <Icon className="h-[18px] w-[18px]" />
      {badge && (
        <span className="absolute top-1.5 right-1.5 h-1.5 w-1.5 rounded-full bg-green-400" />
      )}
    </button>
  );
});

// The controls of an immersive window: nothing shows over the stream until
// Shift is held with the pointer at the top or bottom edge. Then a bar of
// icons slides down from the top and the status bar slides up from the
// bottom, and both stay until Hide. A terminal panel opened from the HUD
// stays (minimized or shown) independently of it.
export default function ImmersiveHud({
  requestFullscreen,
}: {
  requestFullscreen: () => Promise<void>;
}) {
  const [open, setOpen] = useState(false);
  const [fullscreen, setFullscreen] = useState(!!document.fullscreenElement);
  const pointerY = useRef<number | null>(null);
  const { isVirtualKeyboardEnabled, setVirtualKeyboardEnabled } = useHidStore();
  const setDisableVideoFocusTrap = useUiStore(state => state.setDisableVideoFocusTrap);
  const video = useVideoStore(state => state.videoElement);
  const mediaMounted = useMountMediaStore(state => !!state.remoteVirtualMediaState);
  const { terminalType, setTerminalType, guestTerminalSession, usbSerialConsoleEnabled } =
    useUiStore();

  // The terminal this button opens: the target's shell when OS Services
  // offers one, else the KVM's own shell (developer mode), else the USB
  // serial console. The same ones the action bar offers.
  const developerMode = useSettingsStore(state => state.developerMode);
  const osServices = useCapability("os_services");
  const guestOffered = useVideoStore(state => state.guestTerminal);
  const shell = useCapability("shell");
  const usbSerial = useCapability("usb_serial");
  const terminal: AvailableTerminalTypes | null =
    guestOffered && osServices
      ? "guest"
      : developerMode && shell
        ? "kvm"
        : usbSerialConsoleEnabled && usbSerial
          ? "cdcacm"
          : null;

  useEffect(() => {
    const onChange = () => setFullscreen(!!document.fullscreenElement);
    document.addEventListener("fullscreenchange", onChange);
    return () => document.removeEventListener("fullscreenchange", onChange);
  }, []);

  useEffect(() => {
    const nearEdge = (y: number) => y <= EDGE_PX || y >= window.innerHeight - EDGE_PX;
    const onMove = (event: MouseEvent) => {
      pointerY.current = event.clientY;
      if (event.shiftKey && nearEdge(event.clientY)) setOpen(true);
    };
    // Shift pressed while the pointer already rests at an edge. The key
    // still reaches the target, as a lone Shift.
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "Shift" && pointerY.current !== null && nearEdge(pointerY.current)) {
        setOpen(true);
      }
    };
    window.addEventListener("mousemove", onMove, { passive: true });
    window.addEventListener("keydown", onKey, { capture: true, passive: true });
    return () => {
      window.removeEventListener("mousemove", onMove);
      window.removeEventListener("keydown", onKey, { capture: true });
    };
  }, []);

  const hide = useCallback(() => {
    setOpen(false);
    setDisableVideoFocusTrap(false);
    video?.focus();
  }, [setDisableVideoFocusTrap, video]);

  // A shown terminal panel is minimized; a guest shell keeps running.
  const toggleTerminal = () => {
    if (!terminal) return;
    setTerminalType(terminalType === terminal ? "none" : terminal);
  };

  const toggleFullscreen = () => {
    if (document.fullscreenElement) void document.exitFullscreen();
    else void requestFullscreen();
  };

  const normalView = () => {
    const url = new URL(window.location.href);
    url.searchParams.delete("immersive");
    url.searchParams.delete("embed");
    window.location.href = url.toString();
  };

  // Clicks on the HUD stay off the stream.
  const stop = {
    onMouseDown: (e: React.MouseEvent) => e.stopPropagation(),
    onMouseUp: (e: React.MouseEvent) => e.stopPropagation(),
    onClick: (e: React.MouseEvent) => e.stopPropagation(),
    onKeyDown: (e: React.KeyboardEvent) => e.stopPropagation(),
    onKeyUp: (e: React.KeyboardEvent) => e.stopPropagation(),
  };

  const panelClass =
    "z-50 mt-2 w-[420px] max-w-[calc(100vw-2rem)] origin-top transition duration-200 ease-out data-closed:-translate-y-2 data-closed:opacity-0";

  return (
    <>
      <div
        {...stop}
        className={cx(
          "fixed inset-x-0 top-0 z-50 flex justify-center transition-transform duration-200 ease-out",
          open ? "translate-y-0" : "pointer-events-none -translate-y-full",
        )}
      >
        <div className="mt-2 flex items-center gap-x-1 rounded-lg border border-white/10 bg-slate-900/85 p-1 shadow-xl backdrop-blur">
          <HudIcon label={m.immersive_hide()} icon={LuChevronsUp} onClick={hide} />
          <div className="mx-0.5 h-6 w-px bg-white/15" />
          <HudIcon
            label={fullscreen ? m.immersive_exit_fullscreen() : m.action_bar_fullscreen()}
            icon={fullscreen ? LuMinimize : LuMaximize}
            onClick={toggleFullscreen}
          />
          <Popover>
            <PopoverButton as={Fragment}>
              <HudIcon
                label={m.paste_text()}
                icon={MdOutlineContentPasteGo}
                onClick={() => setDisableVideoFocusTrap(true)}
              />
            </PopoverButton>
            <PopoverPanel anchor="bottom" transition className={panelClass}>
              <PasteModal />
            </PopoverPanel>
          </Popover>
          <Popover>
            <PopoverButton as={Fragment}>
              <HudIcon
                label={m.action_bar_virtual_media()}
                icon={LuHardDrive}
                active={mediaMounted}
                onClick={() => setDisableVideoFocusTrap(true)}
              />
            </PopoverButton>
            <PopoverPanel anchor="bottom" transition className={panelClass}>
              <MountPopopover />
            </PopoverPanel>
          </Popover>
          <HudIcon
            label={m.action_bar_virtual_keyboard()}
            icon={FaKeyboard}
            active={isVirtualKeyboardEnabled}
            onClick={() => setVirtualKeyboardEnabled(!isVirtualKeyboardEnabled)}
          />
          {terminal && (
            <HudIcon
              label={
                terminal === "guest"
                  ? guestTerminalSession && terminalType !== "guest"
                    ? m.guest_terminal_running()
                    : m.guest_terminal()
                  : terminal === "kvm"
                    ? m.kvm_terminal()
                    : "USB Serial Console"
              }
              icon={LuTerminal}
              active={terminalType === terminal}
              badge={terminal === "guest" && guestTerminalSession && terminalType !== "guest"}
              onClick={toggleTerminal}
            />
          )}
          <HudIcon
            label={m.capture_screenshot()}
            icon={LuCamera}
            onClick={() =>
              void takeScreenshot(video).catch((error: unknown) =>
                notifications.error(error instanceof Error ? error.message : String(error)),
              )
            }
          />
          <div className="mx-0.5 h-6 w-px bg-white/15" />
          <HudIcon label={m.immersive_normal_view()} icon={LuPanelTop} onClick={normalView} />
        </div>
      </div>

      <div
        {...stop}
        className={cx(
          "fixed inset-x-0 bottom-0 z-50 transition-transform duration-200 ease-out",
          // A shown terminal panel takes the bottom; the status bar would
          // cover its prompt line.
          open && terminalType === "none"
            ? "translate-y-0"
            : "pointer-events-none translate-y-full",
        )}
      >
        <InfoBar />
      </div>
    </>
  );
}
