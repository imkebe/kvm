import { useState } from "react";

import { Button, LinkButton } from "@components/Button";
import { InputFieldWithLabel } from "@components/InputField";
import { Row, useNetbirdCall, useNetbirdState } from "@components/NetbirdCard";
import { m } from "@localizations/messages.js";

// USB Ethernet in NetBird mode: the target computer is its own NetBird peer.
// The KVM runs that peer and hands its address to the target over USB.
export function NetbirdTargetPanel({ usbRunning }: { usbRunning: boolean }) {
  const { state, setState } = useNetbirdState();
  const { busy, call } = useNetbirdCall(setState);
  const [setupKey, setSetupKey] = useState("");
  const [hostname, setHostname] = useState("");

  if (!state) return null;
  const target = state.target;
  const kvmJoined = state.installed && state.enabled && !!state.status?.ip;

  let content;
  if (!kvmJoined) {
    content = (
      <div className="flex items-center justify-between gap-x-3">
        <p className="text-xs text-slate-600 dark:text-slate-400">{m.netbird_target_needs_kvm()}</p>
        <LinkButton to="../../access" size="XS" theme="light" text={m.network_vpn_open()} />
      </div>
    );
  } else if (!target?.enabled) {
    content = (
      <div className="space-y-2">
        <p className="text-xs text-slate-600 dark:text-slate-400">{m.netbird_target_intro()}</p>
        <InputFieldWithLabel
          size="SM"
          label={m.netbird_target_name_label()}
          placeholder={m.netbird_target_name_placeholder()}
          value={hostname}
          onChange={e => setHostname(e.target.value)}
        />
        <InputFieldWithLabel
          size="SM"
          type="password"
          autoComplete="off"
          label={m.netbird_setup_key_label()}
          description={m.netbird_target_key_description()}
          value={setupKey}
          onChange={e => setSetupKey(e.target.value)}
        />
        <Button
          size="SM"
          theme="primary"
          text={m.netbird_target_join()}
          loading={busy}
          disabled={busy || !setupKey.trim()}
          onClick={() =>
            call(
              "netbirdTargetJoin",
              { setupKey: setupKey.trim(), hostname: hostname.trim() },
              () => setSetupKey(""),
            )
          }
        />
      </div>
    );
  } else {
    const status = target.status;
    let line: string;
    if (!usbRunning) line = m.usb_network_share_when_running();
    else if (target.address) line = m.netbird_target_active({ address: target.address });
    else line = m.netbird_target_waiting();
    content = (
      <div className="space-y-1">
        <Row label={m.netbird_target_name_label()} value={target.hostname || "—"} small />
        <Row label={m.netbird_ip()} value={status?.ip || "—"} mono small />
        <Row label={m.netbird_target_state()} value={status?.daemonStatus || "—"} small />
        <p className="pt-1 text-xs text-slate-600 dark:text-slate-400">{line}</p>
        {usbRunning && target.address && target.dnsZone && target.dnsServer && (
          <p className="text-xs text-slate-600 dark:text-slate-400">
            {m.netbird_target_dns({ zone: target.dnsZone, server: target.dnsServer })}
          </p>
        )}
        <div className="pt-1">
          <Button
            size="XS"
            theme="light"
            text={m.netbird_leave()}
            disabled={busy}
            onClick={() => call("netbirdTargetLeave", {})}
          />
        </div>
      </div>
    );
  }

  return (
    <div className="ml-2 space-y-2 border-l-2 border-slate-200 pl-4 dark:border-slate-700">
      {content}
      {target?.error && (
        <p className="text-xs break-words text-red-600 dark:text-red-400">{target.error}</p>
      )}
    </div>
  );
}
