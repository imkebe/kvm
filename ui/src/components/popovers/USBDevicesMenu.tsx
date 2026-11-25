import { useCallback, useEffect, useId, useLayoutEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { LuCamera, LuMic, LuEthernetPort, LuUsb, LuChevronDown } from "react-icons/lu";
import { Button } from "@components/Button";
import { cx } from "@/cva.config";
import { useCapability, useUiStore, useUsbFunctionsStore, type UsbDeviceMenu } from "@hooks/stores";
import { useVirtualCamera } from "@hooks/VirtualCameraProvider";
import { useVirtualMicrophone } from "@hooks/VirtualMicrophoneProvider";
import USBDevicesPopover from "./USBDevicesPopover";

export default function USBDevicesMenu() {
  const { usbDeviceMenu: device, setUsbDeviceMenu: setDevice } = useUiStore();
  const open = device !== null;
  const camera = useVirtualCamera();
  const microphone = useVirtualMicrophone();
  const microphoneSupported = useCapability("usb_virtual_microphone");
  const buttons = useRef<HTMLDivElement>(null);
  const button = useRef<HTMLButtonElement | null>(null);
  const panel = useRef<HTMLDivElement>(null);
  const id = useId();
  const [position, setPosition] = useState({ left: 12, top: 60 });
  const close = useCallback(() => setDevice(null), [setDevice]);
  const usbFunctions = useUsbFunctionsStore(state => state.usbFunctions);
  // On narrow screens the devices share one "USB" button and its list.
  const [listOpen, setListOpen] = useState(false);

  useEffect(() => () => setDevice(null), [setDevice]);

  // The ribbon buttons: one for each USB device that has settings. A dot
  // shows that the device is in use; the camera's button shows the frame
  // size and rate the camera offers the target and the target opened it
  // with, not the rate the browser sends to the KVM (often lower: the KVM
  // repeats frames to keep the target's rate).
  const target = camera.state;
  const cameraText =
    camera.running && target?.enabled && target.width > 0
      ? `Camera ${target.width}×${target.height} · ${target.fps} fps`
      : "Camera";
  const items: {
    device: UsbDeviceMenu;
    text: string;
    label: string;
    icon: React.FC<{ className: string | undefined }>;
    active: boolean;
    // The USB function is on; when it is off the button stays usable (its
    // panel can switch it on) but looks unavailable.
    enabled: boolean;
  }[] = [
    {
      device: "camera",
      text: cameraText,
      label: "USB Camera",
      icon: LuCamera,
      active: camera.running,
      enabled: usbFunctions?.video !== false,
    },
    ...(microphoneSupported
      ? [
          {
            device: "microphone" as const,
            text: microphone.running && microphone.muted ? "Microphone muted" : "Microphone",
            label: "USB Microphone",
            icon: LuMic,
            active: microphone.running && !microphone.muted,
            enabled: usbFunctions?.microphone !== false,
          },
        ]
      : []),
    {
      device: "network",
      text: "USB Ethernet",
      label: "USB Ethernet",
      icon: LuEthernetPort,
      active: false,
      enabled: usbFunctions?.network !== false,
    },
  ];
  const offTitle = "Not on in the USB features; open to switch it on";

  useLayoutEffect(() => {
    if (!open) return;
    const place = () => {
      const anchor = button.current?.getBoundingClientRect();
      if (!anchor) return;
      const width = Math.min(440, window.innerWidth - 24);
      setPosition({
        left: Math.max(12, Math.min(anchor.right - width, window.innerWidth - width - 12)),
        top: anchor.bottom + 8,
      });
    };
    place();
    panel.current?.querySelector<HTMLButtonElement>("button")?.focus();
    window.addEventListener("resize", place);
    window.addEventListener("scroll", place, true);
    return () => {
      window.removeEventListener("resize", place);
      window.removeEventListener("scroll", place, true);
    };
  }, [open, device]);

  useEffect(() => {
    if (!open) return;
    // Native select menus on macOS can temporarily take focus outside the DOM.
    // Dismiss on intentional outside clicks, never on blur or pointer movement.
    const outside = (event: PointerEvent) => {
      if (!(event.target instanceof Node)) return;
      if (!buttons.current?.contains(event.target) && !panel.current?.contains(event.target))
        close();
    };
    const escape = (event: KeyboardEvent) => {
      if (event.key !== "Escape") return;
      event.preventDefault();
      event.stopPropagation();
      close();
      button.current?.focus();
    };
    document.addEventListener("pointerdown", outside, true);
    document.addEventListener("keydown", escape, true);
    return () => {
      document.removeEventListener("pointerdown", outside, true);
      document.removeEventListener("keydown", escape, true);
    };
  }, [open, close]);

  // Close the narrow list on an outside click too.
  useEffect(() => {
    if (!listOpen) return;
    const outside = (event: PointerEvent) => {
      if (event.target instanceof Node && !buttons.current?.contains(event.target))
        setListOpen(false);
    };
    document.addEventListener("pointerdown", outside, true);
    return () => document.removeEventListener("pointerdown", outside, true);
  }, [listOpen]);

  const icon = (item: (typeof items)[number]) =>
    function ItemIcon({ className }: { className: string | undefined }) {
      return (
        <>
          <item.icon className={className} />
          <div
            className={cx(className, "h-2 w-2 rounded-full bg-green-500", {
              hidden: !item.active,
            })}
          />
        </>
      );
    };
  const anyActive = items.some(item => item.active);

  return (
    <div ref={buttons} className="flex flex-wrap items-center gap-x-2 gap-y-2">
      <div className="hidden flex-wrap items-center gap-x-2 gap-y-2 md:flex">
        {items.map(item => (
          <div
            key={item.device}
            className={cx({ "opacity-50": !item.enabled })}
            title={item.enabled ? undefined : offTitle}
          >
            <Button
              size="XS"
              theme="light"
              text={item.text}
              LeadingIcon={icon(item)}
              aria-expanded={device === item.device}
              aria-controls={id}
              aria-haspopup="dialog"
              aria-label={item.label}
              onClick={event => {
                button.current = event.currentTarget as HTMLButtonElement;
                setDevice(device === item.device ? null : item.device);
              }}
            />
          </div>
        ))}
      </div>
      <div className="relative md:hidden">
        <Button
          size="XS"
          theme="light"
          text="USB"
          LeadingIcon={({ className }) => (
            <>
              <LuUsb className={className} />
              <div
                className={cx(className, "h-2 w-2 rounded-full bg-green-500", {
                  hidden: !anyActive,
                })}
              />
            </>
          )}
          TrailingIcon={LuChevronDown}
          aria-haspopup="menu"
          aria-expanded={listOpen}
          aria-label="USB features"
          onClick={event => {
            button.current = event.currentTarget as HTMLButtonElement;
            setListOpen(previous => !previous);
          }}
        />
        {listOpen && (
          <div
            role="menu"
            className="absolute left-0 z-30 mt-1 w-52 rounded-md border border-slate-800/20 bg-white p-1 shadow-lg dark:border-slate-300/20 dark:bg-slate-800"
          >
            {items.map(item => (
              <button
                key={item.device}
                type="button"
                role="menuitem"
                title={item.enabled ? undefined : offTitle}
                className={cx(
                  "flex w-full items-center gap-2 rounded px-2 py-1.5 text-left text-sm hover:bg-slate-100 dark:text-white dark:hover:bg-slate-700",
                  { "opacity-50": !item.enabled },
                )}
                onClick={() => {
                  setListOpen(false);
                  setDevice(item.device);
                }}
              >
                <item.icon className="h-4 w-4 shrink-0" />
                <span className="truncate">{item.text}</span>
                {item.active && <span className="ml-auto h-2 w-2 rounded-full bg-green-500" />}
              </button>
            ))}
          </div>
        )}
      </div>
      {device &&
        createPortal(
          <div
            ref={panel}
            id={id}
            role="dialog"
            aria-label={items.find(item => item.device === device)?.label}
            className="fixed z-30 w-[min(440px,calc(100vw-24px))]"
            style={position}
            onKeyDown={event => event.stopPropagation()}
            onKeyUp={event => event.stopPropagation()}
          >
            <USBDevicesPopover device={device} onClose={close} />
          </div>,
          document.body,
        )}
    </div>
  );
}
