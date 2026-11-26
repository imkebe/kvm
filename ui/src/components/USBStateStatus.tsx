import React, { useEffect, useState } from "react";

import { cx } from "@/cva.config";
import KeyboardAndMouseConnectedIcon from "@assets/keyboard-and-mouse-connected.png";
import { USBStates, useJigglerStore } from "@hooks/stores";
import { useJsonRpc, type JsonRpcResponse } from "@hooks/useJsonRpc";
import { m } from "@localizations/messages.js";
import LoadingSpinner from "@components/LoadingSpinner";
import StatusCard from "@components/StatusCards";
import notifications from "@/notifications";

type StatusProps = Record<
  USBStates,
  {
    icon: React.FC<{ className: string | undefined }>;
    iconClassName: string;
    statusIndicatorClassName: string;
  }
>;

const USBStateMap: Record<USBStates, string> = {
  configured: m.usb_state_connected(),
  attached: m.usb_state_connecting(),
  addressed: m.usb_state_connecting(),
  "not attached": m.usb_state_disconnected(),
  suspended: m.usb_state_low_power_mode(),
};
const StatusCardProps: StatusProps = {
  configured: {
    icon: ({ className }) => (
      <img className={cx(className)} src={KeyboardAndMouseConnectedIcon} alt="" />
    ),
    iconClassName: "h-5 w-5 shrink-0",
    statusIndicatorClassName: "bg-green-500 border-green-600",
  },
  attached: {
    icon: ({ className }) => <LoadingSpinner className={cx(className)} />,
    iconClassName: "h-5 w-5 text-blue-500",
    statusIndicatorClassName: "bg-slate-300 border-slate-400",
  },
  addressed: {
    icon: ({ className }) => <LoadingSpinner className={cx(className)} />,
    iconClassName: "h-5 w-5 text-blue-500",
    statusIndicatorClassName: "bg-slate-300 border-slate-400",
  },
  "not attached": {
    icon: ({ className }) => (
      <img className={cx(className)} src={KeyboardAndMouseConnectedIcon} alt="" />
    ),
    iconClassName: "h-5 w-5 opacity-50 grayscale filter",
    statusIndicatorClassName: "bg-slate-300 border-slate-400",
  },
  suspended: {
    icon: ({ className }) => (
      <img className={cx(className)} src={KeyboardAndMouseConnectedIcon} alt="" />
    ),
    iconClassName: "h-5 w-5 opacity-50 grayscale filter",
    statusIndicatorClassName: "bg-green-500 border-green-600",
  },
};

// A mouse with motion marks on both sides: the mouse jiggler.
function MouseJiggleIcon({ className }: { className?: string }) {
  return (
    <svg
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth={2}
      strokeLinecap="round"
      strokeLinejoin="round"
      className={className}
      aria-hidden="true"
    >
      <rect x="8" y="4" width="8" height="15" rx="4" />
      <path d="M12 7.5v2.5" />
      <path d="M4.5 8.5c-1.3 2-1.3 4 0 6" />
      <path d="M2 7c-2 3-2 6 0 9" opacity={0.55} />
      <path d="M19.5 8.5c1.3 2 1.3 4 0 6" />
      <path d="M22 7c2 3 2 6 0 9" opacity={0.55} />
    </svg>
  );
}

// How long the button rings after the KVM moved the mouse.
const jiggleRingMs = 2500;

// A button at the card's right that turns the KVM's mouse jiggler on and
// off. While the jiggler is on the icon pulses, and it rings each time the
// KVM moves the mouse (jigglerJiggled event). The state is read every 30 s
// (MCP or Home Assistant can change it too) and set at once by the mouse
// settings.
function JigglerButton({ connected }: { connected: boolean }) {
  const { send } = useJsonRpc();
  const jigglerEnabled = useJigglerStore(state => state.jigglerEnabled);
  const setJigglerEnabled = useJigglerStore(state => state.setJigglerEnabled);
  const lastJiggle = useJigglerStore(state => state.jigglerLastJiggle);
  const [busy, setBusy] = useState(false);
  const [ringing, setRinging] = useState(false);

  useEffect(() => {
    if (!connected) return;
    const poll = () =>
      send("getJigglerState", {}, (resp: JsonRpcResponse) => {
        if (!("error" in resp)) setJigglerEnabled(resp.result as boolean);
      });
    poll();
    // A background tab does not poll; it polls at once when shown again.
    const onVisible = () => !document.hidden && poll();
    const timer = window.setInterval(onVisible, 30_000);
    document.addEventListener("visibilitychange", onVisible);
    return () => {
      window.clearInterval(timer);
      document.removeEventListener("visibilitychange", onVisible);
    };
  }, [connected, send, setJigglerEnabled]);

  useEffect(() => {
    if (!lastJiggle || Date.now() - lastJiggle > jiggleRingMs) return;
    setRinging(true);
    const timer = window.setTimeout(() => setRinging(false), jiggleRingMs);
    return () => window.clearTimeout(timer);
  }, [lastJiggle]);

  if (!connected) return null;

  const toggle = () => {
    const next = !jigglerEnabled;
    setBusy(true);
    send("setJigglerState", { enabled: next }, (resp: JsonRpcResponse) => {
      setBusy(false);
      if ("error" in resp) {
        notifications.error(
          m.usb_jiggler_failed({ error: String(resp.error.data || m.unknown_error()) }),
        );
        return;
      }
      setJigglerEnabled(next);
    });
  };

  const label = jigglerEnabled ? m.usb_jiggler_turn_off() : m.usb_jiggler_turn_on();
  const title = ringing
    ? m.usb_jiggler_jiggled()
    : `${jigglerEnabled ? m.usb_jiggler_on() : m.usb_jiggler_off()} · ${label}`;
  return (
    <button
      type="button"
      onClick={toggle}
      disabled={busy}
      aria-pressed={jigglerEnabled}
      aria-label={label}
      title={title}
      className={cx(
        "relative flex h-6 w-6 items-center justify-center rounded transition-colors disabled:opacity-50",
        jigglerEnabled
          ? "bg-blue-50 text-blue-600 hover:bg-blue-100 dark:bg-blue-900/40 dark:text-blue-300 dark:hover:bg-blue-900/70"
          : "text-slate-400 hover:bg-slate-100 hover:text-slate-600 dark:text-slate-500 dark:hover:bg-slate-700 dark:hover:text-slate-300",
      )}
    >
      {ringing && (
        <span className="absolute inset-0 animate-ping rounded bg-blue-400/50" aria-hidden="true" />
      )}
      <MouseJiggleIcon
        className={cx("relative h-4 w-4", { "animate-pulse": jigglerEnabled && !ringing })}
      />
    </button>
  );
}

export default function USBStateStatus({
  state,
  peerConnectionState,
}: {
  state: USBStates;
  peerConnectionState?: RTCPeerConnectionState | null;
}) {
  const props = StatusCardProps[state];
  if (!props) {
    console.warn("Unsupported USB state: ", state);
    return;
  }

  // If the peer connection is not connected, show the USB cable as disconnected
  if (peerConnectionState !== "connected") {
    const { icon: Icon, iconClassName, statusIndicatorClassName } = StatusCardProps["not attached"];

    return (
      <StatusCard
        title={m.usb()}
        status={m.usb_state_disconnected()}
        icon={Icon}
        iconClassName={iconClassName}
        statusIndicatorClassName={statusIndicatorClassName}
      />
    );
  }

  return (
    <StatusCard
      title={m.usb()}
      status={USBStateMap[state]}
      {...StatusCardProps[state]}
      action={<JigglerButton connected={state === "configured"} />}
    />
  );
}
