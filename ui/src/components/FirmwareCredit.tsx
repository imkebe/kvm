import { m } from "@localizations/messages.js";

// The words next to the JetKVM logo that mark this build as the customized
// firmware.
export default function FirmwareCredit() {
  return (
    <span className="text-xs leading-none font-medium whitespace-nowrap text-slate-500 dark:text-slate-400">
      {m.firmware_custom_credit()}
    </span>
  );
}
