import { useCallback, useEffect, useState } from "react";

import { Button, LinkButton } from "@components/Button";
import { GridCard } from "@components/Card";
import { InputFieldWithLabel } from "@components/InputField";
import { NestedSettingsGroup } from "@components/NestedSettingsGroup";
import { SelectMenuBasic } from "@components/SelectMenuBasic";
import { SettingsItem } from "@components/SettingsItem";
import { NetbirdState, useNetbirdStore } from "@hooks/stores";
import { JsonRpcResponse, useJsonRpc } from "@hooks/useJsonRpc";
import { m } from "@localizations/messages.js";
import notifications from "@/notifications";

const cloudURL = "https://api.netbird.io:443";
const signInStates = ["NeedsLogin", "LoginFailed", "SessionExpired"];

// One NetBird action: the reply replaces the state; an error shows as a notice.
export function useNetbirdCall(setState: (state: NetbirdState) => void) {
  const { send } = useJsonRpc();
  const [busy, setBusy] = useState(false);
  const call = useCallback(
    (method: string, params: Record<string, unknown>, onDone?: () => void) => {
      setBusy(true);
      send(method, params, resp => {
        setBusy(false);
        if ("error" in resp) {
          notifications.error(m.netbird_action_failed({ error: errorText(resp) }));
          return;
        }
        setState(resp.result as NetbirdState);
        onDone?.();
      });
    },
    [send, setState],
  );
  return { busy, call };
}

// The NetBird state from the device: loaded once, then kept current by the
// netbirdState events handled in devices.$id.tsx.
export function useNetbirdState() {
  const { send } = useJsonRpc();
  const state = useNetbirdStore(s => s.state);
  const setState = useNetbirdStore(s => s.setState);
  const refresh = useCallback(() => {
    send("getNetbirdState", {}, resp => {
      if (!("error" in resp)) setState(resp.result as NetbirdState);
    });
  }, [send, setState]);
  useEffect(() => {
    refresh();
  }, [refresh]);
  return { state, setState };
}

export function netbirdJoined(state: NetbirdState | null) {
  return !!state?.enabled && !!state.status && !signInStates.includes(state.status.daemonStatus);
}

export function netbirdStateLabel(state: NetbirdState | null): string {
  if (!state?.enabled) return m.netbird_state_off();
  if (state.joining) return m.netbird_state_signing_in();
  switch (state.status?.daemonStatus) {
    case "Connected":
      return m.netbird_state_connected();
    case "Connecting":
      return m.netbird_state_connecting();
    case "Idle":
      return m.netbird_state_disconnected();
    case "NeedsLogin":
      return m.netbird_state_not_joined();
    case "LoginFailed":
      return m.netbird_state_login_failed();
    case "SessionExpired":
      return m.netbird_state_session_expired();
    default:
      return state.daemonRunning ? m.netbird_state_connecting() : m.netbird_state_starting();
  }
}

export function errorText(resp: JsonRpcResponse) {
  if (!("error" in resp)) return "";
  return typeof resp.error.data === "string" ? resp.error.data : resp.error.message;
}

// NetBird as a thin client: install, join, leave, and share with the target
// on the USB Ethernet adapter. Everything else is done in the NetBird dashboard.
export default function NetbirdCard() {
  const { state, setState } = useNetbirdState();
  const { busy, call } = useNetbirdCall(setState);
  const [custom, setCustom] = useState(false);
  const [managementURL, setManagementURL] = useState("");
  const [setupKey, setSetupKey] = useState("");

  // Follow the saved management URL until the owner edits it.
  const savedURL = state?.managementURL;
  useEffect(() => {
    if (!savedURL) return;
    const isCustom = savedURL !== cloudURL;
    setCustom(isCustom);
    setManagementURL(isCustom ? savedURL : "");
  }, [savedURL]);

  if (!state) return null;

  const status = state.status;
  const joined = netbirdJoined(state);
  const percent =
    state.install.total > 0 ? Math.round((state.install.received / state.install.total) * 100) : 0;

  const join = () =>
    call(
      "netbirdJoin",
      { managementURL: custom ? managementURL.trim() : "", setupKey: setupKey.trim() },
      () => setSetupKey(""),
    );

  return (
    <GridCard>
      <div className="animate-fadeIn p-4 text-black opacity-0 animation-duration-500 dark:text-white">
        <div className="space-y-3">
          <div className="flex items-center justify-between gap-x-2">
            <div className="flex items-center gap-x-2">
              <h3 className="text-base font-bold text-slate-900 dark:text-white">
                {m.netbird_title()}
              </h3>
              {state.installed && <Badge state={state} />}
            </div>
            {state.installed && (
              <button
                type="button"
                className="text-xs text-slate-500 hover:text-red-600 disabled:opacity-50 dark:text-slate-400 dark:hover:text-red-400"
                disabled={busy || state.install.active}
                onClick={() => {
                  if (window.confirm(m.netbird_remove_confirm())) call("removeNetbird", {});
                }}
              >
                {m.netbird_remove()}
              </button>
            )}
          </div>

          {!state.installed && (
            <div className="space-y-2">
              <p className="text-sm text-slate-600 dark:text-slate-400">
                {m.netbird_not_installed({ version: state.pinnedVersion })}
              </p>
              {state.install.active ? (
                <div
                  role="progressbar"
                  aria-valuemin={0}
                  aria-valuemax={100}
                  aria-valuenow={percent}
                  className="h-2.5 w-full overflow-hidden rounded-full bg-slate-300 dark:bg-slate-600"
                >
                  <div
                    className="h-2.5 rounded-full bg-blue-700 transition-all duration-500 dark:bg-blue-500"
                    style={{ width: `${percent}%` }}
                  />
                </div>
              ) : (
                <Button
                  size="SM"
                  theme="primary"
                  type="button"
                  text={m.netbird_install()}
                  disabled={busy}
                  onClick={() => call("installNetbird", {})}
                />
              )}
              {state.install.error && (
                <p className="text-sm text-red-600 dark:text-red-400">
                  {m.netbird_install_failed({ error: state.install.error })}
                </p>
              )}
            </div>
          )}

          {state.installed && state.joining && (
            <div className="space-y-2 border-t border-slate-800/10 pt-3 dark:border-slate-300/20">
              <p className="text-sm text-slate-600 dark:text-slate-400">
                {state.authURL ? m.netbird_sign_in_description() : m.netbird_waiting_link()}
              </p>
              {state.authURL && (
                <a
                  href={state.authURL}
                  target="_blank"
                  rel="noopener noreferrer"
                  className="inline-block text-sm font-medium break-all text-blue-500 hover:text-blue-600 dark:text-blue-400 dark:hover:text-blue-300"
                >
                  {state.authURL}
                </a>
              )}
              <div>
                <Button
                  size="SM"
                  theme="light"
                  type="button"
                  text={m.netbird_cancel()}
                  disabled={busy}
                  onClick={() => call("netbirdLeave", {})}
                />
              </div>
            </div>
          )}

          {state.installed && !state.joining && !joined && (
            <div className="space-y-4 border-t border-slate-800/10 pt-3 dark:border-slate-300/20">
              <SettingsItem
                size="SM"
                title={m.netbird_management_title()}
                description={m.netbird_management_description()}
              >
                <SelectMenuBasic
                  size="XS"
                  label=""
                  value={custom ? "custom" : "cloud"}
                  onChange={(e: React.ChangeEvent<HTMLSelectElement>) =>
                    setCustom(e.target.value === "custom")
                  }
                  options={[
                    { value: "cloud", label: m.netbird_management_cloud() },
                    { value: "custom", label: m.netbird_management_custom() },
                  ]}
                />
              </SettingsItem>
              <NestedSettingsGroup>
                {custom && (
                  <InputFieldWithLabel
                    size="SM"
                    label={m.netbird_management_url_label()}
                    placeholder="https://netbird.example.com"
                    value={managementURL}
                    onChange={e => setManagementURL(e.target.value)}
                  />
                )}
                <InputFieldWithLabel
                  size="SM"
                  type="password"
                  autoComplete="off"
                  label={m.netbird_setup_key_label()}
                  description={m.netbird_setup_key_description()}
                  value={setupKey}
                  onChange={e => setSetupKey(e.target.value)}
                />
                <div className="flex items-center gap-x-2">
                  <Button
                    size="SM"
                    theme="primary"
                    type="button"
                    text={busy ? m.netbird_joining() : m.netbird_join()}
                    disabled={busy || (custom && !managementURL.trim())}
                    onClick={join}
                  />
                  {state.enabled && (
                    <Button
                      size="SM"
                      theme="light"
                      type="button"
                      text={m.netbird_turn_off()}
                      disabled={busy}
                      onClick={() => call("netbirdLeave", {})}
                    />
                  )}
                </div>
              </NestedSettingsGroup>
            </div>
          )}

          {joined && status && (
            <div className="space-y-2 border-t border-slate-800/10 pt-3 dark:border-slate-300/20">
              {status.ip && <Row label={m.netbird_ip()} value={status.ip} mono />}
              {status.fqdn && <Row label={m.netbird_name()} value={status.fqdn} />}
              <Row
                label={m.netbird_peers()}
                value={m.netbird_peers_value({
                  connected: status.peersConnected,
                  total: status.peersTotal,
                })}
              />
              <div className="pt-1">
                <Button
                  size="SM"
                  theme="light"
                  type="button"
                  text={m.netbird_leave()}
                  disabled={busy}
                  onClick={() => call("netbirdLeave", {})}
                />
              </div>
            </div>
          )}

          {joined && (
            <div className="flex items-center justify-between gap-x-3 border-t border-slate-800/10 pt-3 dark:border-slate-300/20">
              <p className="text-xs text-slate-600 dark:text-slate-400">
                {state.usbShare ? m.netbird_usb_shared() : m.netbird_usb_not_shared()}
              </p>
              <LinkButton
                to="../hardware/network"
                size="XS"
                theme="light"
                text={m.netbird_usb_open()}
              />
            </div>
          )}

          {state.error && !state.install.active && (
            <p className="text-sm break-words text-red-600 dark:text-red-400">{state.error}</p>
          )}
        </div>
      </div>
    </GridCard>
  );
}

export function Row({
  label,
  value,
  mono,
  small,
}: {
  label: string;
  value: string;
  mono?: boolean;
  small?: boolean;
}) {
  const text = small ? "text-xs" : "text-sm";
  return (
    <div className="flex justify-between gap-x-4">
      <span className={`${text} text-slate-600 dark:text-slate-400`}>{label}</span>
      <span
        className={
          mono
            ? `font-mono ${small ? "text-xs" : "text-[13px]"} font-medium`
            : `truncate ${text} font-medium`
        }
      >
        {value}
      </span>
    </div>
  );
}

function Badge({ state }: { state: NetbirdState }) {
  const status = state.status?.daemonStatus;
  const tone =
    state.enabled && status === "Connected"
      ? "bg-green-100 text-green-700 dark:bg-green-900/30 dark:text-green-400"
      : state.enabled
        ? "bg-amber-100 text-amber-700 dark:bg-amber-900/30 dark:text-amber-400"
        : "bg-slate-100 text-slate-600 dark:bg-slate-700 dark:text-slate-400";
  return (
    <span className={`rounded-full px-2 py-0.5 text-xs font-medium ${tone}`}>
      {netbirdStateLabel(state)}
    </span>
  );
}
