import { useEffect } from "react";
import { Popover, PopoverButton, PopoverPanel } from "@headlessui/react";
import { LuActivity } from "react-icons/lu";

import { cx } from "@/cva.config";
import { useUiStore } from "@hooks/stores";
import DeviceStatusPopover from "@components/popovers/DeviceStatusPopover";
import { m } from "@localizations/messages.js";

// While the panel is open, keys go to it rather than to the video.
function Panel() {
  const setDisableVideoFocusTrap = useUiStore(state => state.setDisableVideoFocusTrap);
  useEffect(() => {
    setDisableVideoFocusTrap(true);
    return () => setDisableVideoFocusTrap(false);
  }, [setDisableVideoFocusTrap]);
  return <DeviceStatusPopover />;
}

// The square button at the right of the "JetKVM Device" card: it opens the
// KVM's status (processor, temperature, memory, MCP), read only while open.
export default function DeviceStatusButton() {
  return (
    <Popover>
      <PopoverButton
        title={m.action_bar_device_status()}
        aria-label={m.action_bar_device_status()}
        className="flex h-7 w-7 items-center justify-center rounded-md bg-blue-700 text-white shadow-sm transition-colors hover:bg-blue-800 focus:outline-none focus-visible:ring-2 focus-visible:ring-blue-400 data-open:bg-blue-900 dark:bg-blue-600 dark:hover:bg-blue-500"
      >
        <LuActivity className="h-4 w-4" />
      </PopoverButton>
      <PopoverPanel
        anchor="bottom end"
        transition
        className={cx(
          "z-30 flex w-[min(440px,calc(100vw-24px))] origin-top flex-col overflow-visible! [--anchor-gap:8px]",
          "transition duration-300 ease-out data-closed:translate-y-4 data-closed:opacity-0",
        )}
      >
        <Panel />
      </PopoverPanel>
    </Popover>
  );
}
