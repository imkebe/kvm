import { test, expect } from "bun:test";
import {
  usbTrafficRate,
  formatUSBBytes,
  type USBNetworkSample,
} from "../src/utils/usbNetworkMetrics";
const sample: USBNetworkSample = {
  interface_name: "usb0",
  sampled_at: 1000,
  stats_available: true,
  rx_bytes: 1000,
  tx_bytes: 500,
};
test("uses device timestamps and separates target traffic directions", () => {
  expect(
    usbTrafficRate(sample, { ...sample, sampled_at: 3000, rx_bytes: 5096, tx_bytes: 2548 }),
  ).toEqual({ date: 3000, fromTarget: 2048, toTarget: 1024 });
});
test("reset, absent, stale and disconnected counters cannot invent traffic", () => {
  expect(usbTrafficRate(null, sample)).toBeNull();
  for (const current of [
    { ...sample, stats_available: false },
    { ...sample, interface_name: "usb1" },
    { ...sample, rx_bytes: 0 },
    { ...sample, tx_bytes: 0 },
    { ...sample, sampled_at: 0 },
    { ...sample, sampled_at: 12000 },
    sample,
  ])
    expect(usbTrafficRate(sample, current)).toBeNull();
  expect(
    usbTrafficRate({ ...sample, stats_available: false }, { ...sample, sampled_at: 2000 }),
  ).toBeNull();
});
test("formats byte rates without claiming bits", () => {
  expect(formatUSBBytes(512)).toBe("512 B");
  expect(formatUSBBytes(2048)).toBe("2.0 KiB");
  expect(formatUSBBytes(3 * 1024 * 1024)).toBe("3.0 MiB");
});
