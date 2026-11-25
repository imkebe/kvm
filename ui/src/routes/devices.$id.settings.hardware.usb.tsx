import { SettingsPageHeader } from "@components/SettingsPageheader";
import { UsbDeviceSetting } from "@components/UsbDeviceSetting";
import { UsbInfoSetting } from "@components/UsbInfoSetting";
import { UsbFunctionNamesSetting } from "@components/UsbFunctionNamesSetting";
import { m } from "@localizations/messages.js";

export default function SettingsHardwareUsbRoute() {
  return (
    <div className="space-y-4">
      <SettingsPageHeader
        title={m.hardware_usb_title()}
        description={m.hardware_usb_description()}
      />
      <UsbDeviceSetting />
      <UsbFunctionNamesSetting />
      <UsbInfoSetting />
    </div>
  );
}
