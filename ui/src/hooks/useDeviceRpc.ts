import { useCallback } from "react";
import { useRTCStore } from "@hooks/stores";
import { useJsonRpc } from "@hooks/useJsonRpc";

export function useDeviceRpc() {
  const { send } = useJsonRpc();
  const channel = useRTCStore(state => state.rpcDataChannel);
  const request = useCallback(
    <T>(method: string, params: object = {}): Promise<T> => {
      if (channel?.readyState !== "open")
        return Promise.reject(new Error("Device is disconnected"));
      return new Promise((resolve, reject) => {
        const timer = window.setTimeout(() => reject(new Error("Device request timed out")), 15000);
        void send(method, params, response => {
          window.clearTimeout(timer);
          if ("error" in response) reject(new Error(response.error.data || response.error.message));
          else resolve(response.result as T);
        });
      });
    },
    [channel, send],
  );
  return { request, ready: channel?.readyState === "open" };
}
