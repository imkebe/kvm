import { useCallback } from "react";

import { JsonRpcResponse, useJsonRpc } from "@hooks/useJsonRpc";

// The KVM's network discovery RPCs (network_discovery.go).

export interface AddressCheck {
  address: string;
  // Another device answered ARP for the address.
  in_use: boolean;
  mac?: string;
  // The KVM itself has the address.
  own?: boolean;
}

export interface NeighborEntry {
  ip: string;
  mac?: string;
  state: string;
  family: "ipv4" | "ipv6";
  router?: boolean;
}

export interface DiscoveredHost {
  ip: string;
  mac?: string;
  rtt_ms?: number;
  hostname?: string;
  self?: boolean;
}

export type ScanMethod = "arp" | "ping" | "ipv6";

export interface ScanResult {
  method: ScanMethod;
  target: string;
  scanned: number;
  hosts: DiscoveredHost[];
  duration_ms: number;
}

export function useNetworkDiscovery() {
  const { send } = useJsonRpc();
  const call = useCallback(
    <T>(method: string, params: Record<string, unknown> = {}) =>
      new Promise<T>((resolve, reject) => {
        void send(method, params, (response: JsonRpcResponse) => {
          if ("error" in response) {
            reject(new Error(String(response.error.data || response.error.message)));
          } else {
            resolve(response.result as T);
          }
        });
      }),
    [send],
  );
  const checkAddress = useCallback(
    (address: string) => call<AddressCheck>("checkIpv4Address", { address }),
    [call],
  );
  const neighbors = useCallback(() => call<NeighborEntry[]>("getNetworkNeighbors"), [call]);
  const scanDefaults = useCallback(() => call<{ ipv4?: string }>("getNetworkScanDefaults"), [call]);
  const scan = useCallback(
    (method: ScanMethod, target: string) => call<ScanResult>("scanNetwork", { method, target }),
    [call],
  );
  return { checkAddress, neighbors, scanDefaults, scan };
}
