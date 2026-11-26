import { LuTriangleAlert } from "react-icons/lu";

import { cx } from "@/cva.config";
import { useUsbFunctionsStore, type UsbFunctions } from "@hooks/stores";
import { LinkButton } from "@components/Button";
import { m } from "@localizations/messages.js";

// UsbFunctionGate greys out (but keeps usable) settings whose USB function is
// off, with a note and a link to the USB features.
export function UsbFunctionGate({
  fn,
  children,
}: {
  fn: keyof UsbFunctions;
  children: React.ReactNode;
}) {
  const off = useUsbFunctionsStore(state => state.usbFunctions?.[fn] === false);
  return (
    <div className="space-y-3">
      {off && (
        <div className="flex flex-wrap items-center gap-2 rounded-md border border-amber-500/40 bg-amber-50 p-2 text-xs text-amber-800 dark:bg-amber-900/20 dark:text-amber-200">
          <LuTriangleAlert className="h-4 w-4 shrink-0" />
          <span>{m.usb_function_off_note()}</span>
          <LinkButton to="../usb" size="XS" theme="light" text={m.usb_function_open_usb()} />
        </div>
      )}
      <div className={cx({ "opacity-50": off })}>{children}</div>
    </div>
  );
}
