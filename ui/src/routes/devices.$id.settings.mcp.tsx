import { useCallback, useEffect, useMemo, useState } from "react";
import { LuBot, LuCopy, LuInfo, LuTrash2, LuTriangleAlert } from "react-icons/lu";

import { useCapability, type OsServicesSettings } from "@hooks/stores";
import { JsonRpcResponse, useJsonRpc } from "@hooks/useJsonRpc";
import { Button, LinkButton } from "@components/Button";
import Checkbox, { CheckboxWithLabel } from "@components/Checkbox";
import { InputFieldWithLabel } from "@components/InputField";
import { SelectMenuBasic } from "@components/SelectMenuBasic";
import { SettingsItem } from "@components/SettingsItem";
import { SettingsPageHeader } from "@components/SettingsPageheader";
import { SettingsSectionHeader } from "@components/SettingsSectionHeader";
import McpConfigDialog from "@components/McpConfigDialog";
import { useCopyToClipboard } from "@components/useCopyToClipBoard";
import notifications from "@/notifications";
import { m } from "@localizations/messages.js";

type ApiScope = "device" | "terminal" | "metrics";

interface ApiToken {
  id: string;
  name: string;
  prefix: string;
  scopes: ApiScope[];
  created_at: number;
  expires_at: number;
  last_used_at: number;
  expired: boolean;
}

const DAY = 86400;
// A token that expires sooner than this is shown as expiring.
const SOON = 7 * DAY;

function formatDate(unix: number) {
  return new Date(unix * 1000).toLocaleString(undefined, {
    dateStyle: "medium",
    timeStyle: "short",
  });
}

function relativeDays(unix: number, now: number) {
  const days = Math.round((unix - now) / DAY);
  if (days === 0) return m.mcp_token_today();
  if (days === 1) return m.mcp_token_in_one_day();
  return days > 0 ? m.mcp_token_in_days({ days }) : m.mcp_token_days_ago({ days: -days });
}

// A note under a control that cannot be used yet, and why.
function Dependency({ children }: { children: React.ReactNode }) {
  return (
    <p className="flex items-start gap-1.5 text-xs text-amber-700 dark:text-amber-300">
      <LuTriangleAlert className="mt-0.5 h-3.5 w-3.5 shrink-0" />
      <span>{children}</span>
    </p>
  );
}

export default function SettingsMcpRoute() {
  const { send } = useJsonRpc();
  const osServicesCapability = useCapability("os_services");

  const [deviceApi, setDeviceApi] = useState<boolean | null>(null);
  const [mcpServer, setMcpServer] = useState<boolean | null>(null);
  const [osServices, setOsServices] = useState<OsServicesSettings | null>(null);
  const [tokens, setTokens] = useState<ApiToken[]>([]);
  const [now, setNow] = useState(() => Math.floor(Date.now() / 1000));

  const [name, setName] = useState("");
  const [scopes, setScopes] = useState<ApiScope[]>(["device", "terminal"]);
  const [expiryDays, setExpiryDays] = useState("90");
  const [creating, setCreating] = useState(false);
  const [newToken, setNewToken] = useState<{ token: string; name: string } | null>(null);
  const [configOpen, setConfigOpen] = useState(false);
  const { copy: copyText } = useCopyToClipboard(0);

  const loadTokens = useCallback(() => {
    send("getAPITokens", {}, (resp: JsonRpcResponse) => {
      if ("error" in resp) {
        notifications.error(
          m.mcp_tokens_load_failed({ error: String(resp.error.data || m.unknown_error()) }),
        );
        return;
      }
      setTokens(resp.result as ApiToken[]);
      setNow(Math.floor(Date.now() / 1000));
    });
  }, [send]);

  useEffect(() => {
    send("getDeviceAPIEnabled", {}, (resp: JsonRpcResponse) => {
      if (!("error" in resp)) setDeviceApi(resp.result as boolean);
    });
    send("getMCPServerEnabled", {}, (resp: JsonRpcResponse) => {
      // Older firmware has no MCP server: leave the switch out.
      setMcpServer("error" in resp ? null : (resp.result as boolean));
    });
    if (osServicesCapability) {
      send("getOsServicesSettings", {}, (resp: JsonRpcResponse) => {
        if (!("error" in resp)) setOsServices(resp.result as OsServicesSettings);
      });
    }
    loadTokens();
  }, [send, osServicesCapability, loadTokens]);

  // Expiry is shown against the clock; refresh it once a minute.
  useEffect(() => {
    const timer = window.setInterval(() => setNow(Math.floor(Date.now() / 1000)), 60_000);
    return () => window.clearInterval(timer);
  }, []);

  const terminalBlocker = useMemo(() => {
    if (!osServicesCapability) return m.mcp_terminal_needs_firmware();
    if (!osServices) return null;
    if (!osServices.enabled) return m.mcp_terminal_needs_os_services();
    if (!osServices.terminal_enabled) return m.mcp_terminal_needs_terminal_access();
    return null;
  }, [osServicesCapability, osServices]);
  const terminalApi = !!osServices?.terminal_api_enabled;

  const handleDeviceApi = (enabled: boolean) => {
    send("setDeviceAPIEnabled", { enabled }, (resp: JsonRpcResponse) => {
      if ("error" in resp) {
        notifications.error(
          m.advanced_error_device_api({ error: String(resp.error.data || m.unknown_error()) }),
        );
        return;
      }
      setDeviceApi(enabled);
    });
  };

  const handleMcpServer = (enabled: boolean) => {
    send("setMCPServerEnabled", { enabled }, (resp: JsonRpcResponse) => {
      if ("error" in resp) {
        notifications.error(
          m.mcp_server_save_failed({ error: String(resp.error.data || m.unknown_error()) }),
        );
        return;
      }
      setMcpServer(enabled);
    });
  };
  const mcpUrl = `${window.location.protocol}//${window.location.host}/mcp`;

  const handleTerminalApi = (enabled: boolean) => {
    send(
      "setOsServicesSettings",
      { settings: { terminal_api_enabled: enabled } },
      (resp: JsonRpcResponse) => {
        if ("error" in resp) {
          notifications.error(
            m.os_services_save_failed({ error: String(resp.error.data || m.unknown_error()) }),
          );
          return;
        }
        setOsServices(resp.result as OsServicesSettings);
      },
    );
  };

  const toggleScope = (scope: ApiScope, on: boolean) =>
    setScopes(previous =>
      on ? [...previous.filter(s => s !== scope), scope] : previous.filter(s => s !== scope),
    );

  const handleCreate = () => {
    setCreating(true);
    send(
      "createAPIToken",
      { name: name.trim(), scopes, expiresInDays: Number(expiryDays) },
      (resp: JsonRpcResponse) => {
        setCreating(false);
        if ("error" in resp) {
          notifications.error(
            m.mcp_token_create_failed({ error: String(resp.error.data || m.unknown_error()) }),
          );
          return;
        }
        const result = resp.result as { token: string; info: ApiToken };
        setNewToken({ token: result.token, name: result.info.name });
        setName("");
        loadTokens();
      },
    );
  };

  const handleDelete = (token: ApiToken) => {
    if (!window.confirm(m.mcp_token_delete_confirm({ name: token.name }))) return;
    send("deleteAPIToken", { id: token.id }, (resp: JsonRpcResponse) => {
      if ("error" in resp) {
        notifications.error(
          m.mcp_token_delete_failed({ error: String(resp.error.data || m.unknown_error()) }),
        );
        return;
      }
      notifications.success(m.mcp_token_deleted({ name: token.name }));
      loadTokens();
    });
  };

  // navigator.clipboard is missing on a plain HTTP page; the helper falls
  // back to a copy event there.
  const copy = async (text: string) => {
    if (await copyText(text)) notifications.success(m.mcp_copied());
    else notifications.error(m.mcp_copy_failed());
  };

  const scopeLabel: Record<ApiScope, string> = {
    device: m.mcp_scope_device(),
    terminal: m.mcp_scope_terminal(),
    metrics: m.mcp_scope_metrics(),
  };
  const scopeNote: Partial<Record<ApiScope, string | null>> = {
    device: deviceApi === false ? m.mcp_scope_device_off() : null,
    terminal: terminalBlocker ?? (osServices && !terminalApi ? m.mcp_scope_terminal_off() : null),
  };

  return (
    <div className="space-y-4">
      <SettingsPageHeader title={m.mcp_title()} description={m.mcp_description()} />
      <div className="flex flex-wrap items-center justify-between gap-2 rounded-md border border-slate-800/10 p-3 dark:border-slate-300/20">
        <p className="text-sm text-slate-700 dark:text-slate-300">{m.mcp_config_hint()}</p>
        <Button
          size="SM"
          theme="light"
          LeadingIcon={LuBot}
          text={m.mcp_config_button()}
          onClick={() => setConfigOpen(true)}
        />
      </div>
      <McpConfigDialog
        key={newToken?.token ?? "none"}
        open={configOpen}
        onClose={() => setConfigOpen(false)}
        initialToken={newToken?.token ?? ""}
        serverEnabled={!!mcpServer}
        copy={text => void copy(text)}
      />

      {mcpServer !== null && (
        <div className="space-y-4">
          <SettingsSectionHeader
            title={m.mcp_server_section_title()}
            description={m.mcp_server_section_description()}
          />
          <SettingsItem title={m.mcp_server_title()} description={m.mcp_server_description()}>
            <Checkbox checked={mcpServer} onChange={e => handleMcpServer(e.target.checked)} />
          </SettingsItem>
          {mcpServer && (
            <div className="flex flex-wrap items-center gap-2">
              <code className="rounded bg-slate-100 px-2 py-1 font-mono text-xs select-all dark:bg-slate-800">
                {mcpUrl}
              </code>
              <Button
                size="XS"
                theme="light"
                LeadingIcon={LuCopy}
                text={m.mcp_copy()}
                onClick={() => void copy(mcpUrl)}
              />
            </div>
          )}
          {mcpServer && deviceApi === false && !terminalApi && (
            <Dependency>{m.mcp_server_needs_apis()}</Dependency>
          )}
          <div className="h-px w-full bg-slate-800/10 dark:bg-slate-300/20" />
        </div>
      )}

      <div className="space-y-4">
        <SettingsSectionHeader title={m.mcp_apis_title()} description={m.mcp_apis_description()} />
        <SettingsItem
          title={m.advanced_device_api_title()}
          description={m.advanced_device_api_description()}
        >
          <Checkbox
            checked={!!deviceApi}
            disabled={deviceApi === null}
            onChange={e => handleDeviceApi(e.target.checked)}
          />
        </SettingsItem>
        {deviceApi && osServices && !osServices.enabled && (
          <Dependency>{m.mcp_device_needs_os_services_for_some()}</Dependency>
        )}

        <SettingsItem
          title={m.os_services_terminal_api_title()}
          description={m.os_services_terminal_api_description()}
        >
          <Checkbox
            checked={terminalApi}
            disabled={!!terminalBlocker || !osServices}
            onChange={e => handleTerminalApi(e.target.checked)}
          />
        </SettingsItem>
        {terminalBlocker && (
          <div className="space-y-2">
            <Dependency>{terminalBlocker}</Dependency>
            {osServicesCapability && (
              <LinkButton
                to="../os-services"
                size="XS"
                theme="light"
                text={m.mcp_open_os_services()}
              />
            )}
          </div>
        )}
      </div>

      <div className="space-y-4">
        <div className="h-px w-full bg-slate-800/10 dark:bg-slate-300/20" />
        <SettingsSectionHeader
          title={m.mcp_tokens_title()}
          description={m.mcp_tokens_description()}
        />
        {window.location.protocol === "http:" && <Dependency>{m.mcp_http_warning()}</Dependency>}
        <p className="text-xs text-slate-600 dark:text-slate-400">{m.mcp_device_token_warning()}</p>

        {tokens.length === 0 ? (
          <p className="text-sm text-slate-600 dark:text-slate-400">{m.mcp_tokens_none()}</p>
        ) : (
          <ul className="divide-y divide-slate-800/10 rounded-md border border-slate-800/10 dark:divide-slate-300/20 dark:border-slate-300/20">
            {tokens.map(token => {
              const expiring =
                !token.expired && token.expires_at !== 0 && token.expires_at - now < SOON;
              return (
                <li key={token.id} className="flex items-start justify-between gap-3 p-3">
                  <div className="min-w-0 space-y-1">
                    <div className="flex flex-wrap items-center gap-2">
                      <span className="text-sm font-semibold">{token.name}</span>
                      <code className="text-xs text-slate-500 dark:text-slate-400">
                        {token.prefix}…
                      </code>
                      {token.expired && (
                        <span className="rounded bg-red-500 px-1.5 py-0.5 text-[10px] font-semibold text-white">
                          {m.mcp_token_expired()}
                        </span>
                      )}
                      {expiring && (
                        <span className="rounded bg-yellow-500 px-1.5 py-0.5 text-[10px] font-semibold text-white">
                          {m.mcp_token_expiring()}
                        </span>
                      )}
                    </div>
                    <div className="flex flex-wrap gap-1">
                      {token.scopes.map(scope => (
                        <span
                          key={scope}
                          className="rounded bg-blue-50 px-1.5 py-0.5 text-[11px] text-blue-700 dark:bg-blue-900/40 dark:text-blue-200"
                        >
                          {scopeLabel[scope] ?? scope}
                        </span>
                      ))}
                    </div>
                    <p className="text-xs text-slate-600 dark:text-slate-400">
                      {m.mcp_token_created({ date: formatDate(token.created_at) })}
                      {" · "}
                      {token.expires_at === 0
                        ? m.mcp_token_never_expires()
                        : token.expired
                          ? m.mcp_token_expired_on({ date: formatDate(token.expires_at) })
                          : m.mcp_token_expires_on({
                              date: formatDate(token.expires_at),
                              relative: relativeDays(token.expires_at, now),
                            })}
                      {" · "}
                      {token.last_used_at
                        ? m.mcp_token_last_used({ date: formatDate(token.last_used_at) })
                        : m.mcp_token_never_used()}
                    </p>
                  </div>
                  <Button
                    size="XS"
                    theme="light"
                    LeadingIcon={LuTrash2}
                    text={m.mcp_token_delete()}
                    onClick={() => handleDelete(token)}
                  />
                </li>
              );
            })}
          </ul>
        )}

        {newToken && (
          <div className="space-y-2 rounded-md border border-green-600/40 bg-green-50 p-3 dark:bg-green-900/20">
            <p className="text-sm font-semibold text-green-800 dark:text-green-200">
              {m.mcp_token_created_title({ name: newToken.name })}
            </p>
            <p className="text-xs text-green-800 dark:text-green-200">
              {m.mcp_token_created_once()}
            </p>
            <div className="flex items-center gap-2">
              <code className="min-w-0 flex-1 rounded bg-white px-2 py-1 font-mono text-xs break-all select-all dark:bg-slate-900">
                {newToken.token}
              </code>
              <Button
                size="XS"
                theme="light"
                LeadingIcon={LuCopy}
                text={m.mcp_copy()}
                onClick={() => void copy(newToken.token)}
              />
            </div>
            <div className="flex gap-2">
              <Button
                size="XS"
                theme="primary"
                LeadingIcon={LuBot}
                text={m.mcp_config_button()}
                onClick={() => setConfigOpen(true)}
              />
              <Button
                size="XS"
                theme="light"
                text={m.mcp_done()}
                onClick={() => setNewToken(null)}
              />
            </div>
          </div>
        )}

        <div className="space-y-3 rounded-md border border-slate-800/10 p-3 dark:border-slate-300/20">
          <p className="text-sm font-semibold">{m.mcp_token_new()}</p>
          <InputFieldWithLabel
            size="SM"
            label={m.mcp_token_name()}
            placeholder={m.mcp_token_name_placeholder()}
            value={name}
            maxLength={64}
            onChange={e => setName(e.target.value)}
          />
          <div className="space-y-1">
            <p className="text-sm font-medium">{m.mcp_token_scopes()}</p>
            {(["device", "terminal", "metrics"] as ApiScope[]).map(scope => (
              <div key={scope} className="space-y-0.5">
                <CheckboxWithLabel
                  label={scopeLabel[scope]}
                  checked={scopes.includes(scope)}
                  onChange={e => toggleScope(scope, e.target.checked)}
                />
                {scopes.includes(scope) && scopeNote[scope] && (
                  <p className="flex items-start gap-1.5 pl-6 text-xs text-slate-600 dark:text-slate-400">
                    <LuInfo className="mt-0.5 h-3.5 w-3.5 shrink-0" />
                    <span>{scopeNote[scope]}</span>
                  </p>
                )}
              </div>
            ))}
          </div>
          <div className="max-w-xs space-y-1">
            <p className="text-sm font-medium">{m.mcp_token_expiry()}</p>
            <SelectMenuBasic
              size="SM"
              label=""
              value={expiryDays}
              onChange={e => setExpiryDays(e.target.value)}
              options={[
                { value: "1", label: m.mcp_expiry_days({ days: 1 }) },
                { value: "7", label: m.mcp_expiry_days({ days: 7 }) },
                { value: "30", label: m.mcp_expiry_days({ days: 30 }) },
                { value: "90", label: m.mcp_expiry_days({ days: 90 }) },
                { value: "365", label: m.mcp_expiry_days({ days: 365 }) },
                { value: "0", label: m.mcp_expiry_never() },
              ]}
            />
          </div>
          <Button
            size="SM"
            theme="primary"
            loading={creating}
            disabled={!name.trim() || scopes.length === 0 || creating}
            text={m.mcp_token_create()}
            onClick={handleCreate}
          />
        </div>
        <p className="text-xs text-slate-600 dark:text-slate-400">{m.mcp_tokens_footer()}</p>
      </div>
    </div>
  );
}
