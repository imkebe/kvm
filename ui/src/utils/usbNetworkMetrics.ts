export interface USBNetworkSample {
  interface_name: string;
  sampled_at: number;
  stats_available: boolean;
  rx_bytes: number;
  tx_bytes: number;
}

export interface USBTrafficRate {
  date: number;
  fromTarget: number;
  toTarget: number;
}

export function usbTrafficRate(
  previous: USBNetworkSample | null,
  current: USBNetworkSample,
): USBTrafficRate | null {
  if (
    !previous ||
    !previous.stats_available ||
    !current.stats_available ||
    previous.interface_name !== current.interface_name
  )
    return null;
  const seconds = (current.sampled_at - previous.sampled_at) / 1000;
  if (
    seconds <= 0 ||
    seconds > 10 ||
    current.rx_bytes < previous.rx_bytes ||
    current.tx_bytes < previous.tx_bytes
  )
    return null;
  return {
    date: current.sampled_at,
    fromTarget: (current.rx_bytes - previous.rx_bytes) / seconds,
    toTarget: (current.tx_bytes - previous.tx_bytes) / seconds,
  };
}

export function formatUSBBytes(bytes: number) {
  if (bytes < 1024) return `${Math.round(bytes)} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KiB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MiB`;
}
