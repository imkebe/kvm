import { SettingsPageHeader } from "@components/SettingsPageheader";
import { USBNetworkSetting } from "@components/USBNetworkSetting";
import { UsbFunctionGate } from "@components/UsbFunctionGate";
import { m } from "@localizations/messages.js";

export default function SettingsHardwareNetworkRoute() {
  return (
    <div className="space-y-4">
      <SettingsPageHeader
        title={m.hardware_network_title()}
        description={m.hardware_network_description()}
      />
      <UsbFunctionGate fn="network">
        <USBNetworkSetting />
      </UsbFunctionGate>
    </div>
  );
}
