import { SettingsPageHeader } from "@components/SettingsPageheader";
import { VirtualVideoSetting } from "@components/VirtualVideoSetting";
import { UsbFunctionGate } from "@components/UsbFunctionGate";
import { m } from "@localizations/messages.js";

export default function SettingsHardwareCameraRoute() {
  return (
    <div className="space-y-4">
      <SettingsPageHeader
        title={m.hardware_camera_title()}
        description={m.hardware_camera_description()}
      />
      <UsbFunctionGate fn="video">
        <VirtualVideoSetting />
      </UsbFunctionGate>
    </div>
  );
}
