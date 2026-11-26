import { useMemo, useState } from "react";
import { CloseButton } from "@headlessui/react";
import { LuCopy, LuTriangleAlert } from "react-icons/lu";

import { cx } from "@/cva.config";
import { Button } from "@components/Button";
import { InputFieldWithLabel } from "@components/InputField";
import Modal from "@components/Modal";
import { m } from "@localizations/messages.js";

type Client = "claude" | "codex";

// A value as one shell word.
function shellWord(value: string) {
  return /^[\w@%+=:,./~-]+$/.test(value) ? value : `'${value.replaceAll("'", `'\\''`)}'`;
}

// Configuration for an MCP client of the KVM's own MCP server (streamable
// HTTP at /mcp). The KVM shows a token only when it is created, so the token
// is typed in here (or handed over right after creating one).
export default function McpConfigDialog({
  open,
  onClose,
  initialToken,
  serverEnabled,
  copy,
}: {
  open: boolean;
  onClose: () => void;
  initialToken: string;
  // The KVM's MCP server is on.
  serverEnabled: boolean;
  copy: (text: string) => void;
}) {
  const [client, setClient] = useState<Client>("claude");
  const [serverName, setServerName] = useState("jetkvm");
  const [tokenInput, setTokenInput] = useState<string | null>(null);
  const token = (tokenInput ?? initialToken).trim();

  const name = serverName.trim() || "jetkvm";
  const url = `${window.location.protocol}//${window.location.host}/mcp`;
  const bearer = `Bearer ${token || "<API token>"}`;

  const snippets = useMemo(() => {
    if (client === "claude") {
      const json = JSON.stringify(
        { mcpServers: { [name]: { type: "http", url, headers: { Authorization: bearer } } } },
        null,
        2,
      );
      const cli = [
        "claude mcp add --transport http --scope user",
        shellWord(name),
        shellWord(url),
        "--header",
        shellWord(`Authorization: ${bearer}`),
      ].join(" ");
      return [
        { title: m.mcp_config_cli({ client: "Claude Code" }), text: cli },
        { title: m.mcp_config_file({ file: ".mcp.json" }), text: json },
      ];
    }
    // Codex documents no token option for `codex mcp add`: config.toml only.
    const table = `[mcp_servers.${/^[A-Za-z0-9_-]+$/.test(name) ? name : JSON.stringify(name)}]`;
    const withHeader = [
      table,
      `url = ${JSON.stringify(url)}`,
      `http_headers = { Authorization = ${JSON.stringify(bearer)} }`,
    ].join("\n");
    const withEnv = [
      `# export JETKVM_TOKEN=${shellWord(token || "<API token>")}`,
      table,
      `url = ${JSON.stringify(url)}`,
      `bearer_token_env_var = "JETKVM_TOKEN"`,
    ].join("\n");
    return [
      { title: m.mcp_config_file({ file: "~/.codex/config.toml" }), text: withHeader },
      { title: m.mcp_config_codex_env(), text: withEnv },
    ];
  }, [client, name, url, bearer, token]);

  return (
    <Modal open={open} onClose={onClose}>
      <div className="mx-auto max-w-2xl px-4">
        <div className="pointer-events-auto relative w-full overflow-hidden rounded-lg border border-slate-200 bg-white shadow-sm dark:border-slate-800 dark:bg-slate-900">
          <div className="space-y-4 p-6 text-slate-900 dark:text-slate-100">
            <div className="space-y-1">
              <h2 className="font-semibold text-slate-950 dark:text-white">
                {m.mcp_config_title()}
              </h2>
              <p className="text-sm text-slate-700 dark:text-slate-300">
                {m.mcp_config_on_kvm_about({ url })}
              </p>
              {!serverEnabled && (
                <p className="flex items-start gap-1.5 text-xs text-amber-700 dark:text-amber-400">
                  <LuTriangleAlert className="mt-0.5 h-3.5 w-3.5 shrink-0" />
                  <span>{m.mcp_config_server_off()}</span>
                </p>
              )}
              {window.location.protocol === "http:" && (
                <p className="flex items-start gap-1.5 text-xs text-amber-700 dark:text-amber-400">
                  <LuTriangleAlert className="mt-0.5 h-3.5 w-3.5 shrink-0" />
                  <span>{m.mcp_http_warning()}</span>
                </p>
              )}
              <p className="text-xs text-slate-600 dark:text-slate-400">
                {m.mcp_device_token_warning()}
              </p>
              {window.location.protocol === "https:" && (
                <p className="text-xs text-slate-600 dark:text-slate-400">
                  {m.mcp_config_https_note()}
                </p>
              )}
            </div>

            <div className="flex gap-1 rounded-md bg-slate-100 p-1 dark:bg-slate-800">
              {(
                [
                  ["claude", "Claude Code"],
                  ["codex", "Codex"],
                ] as [Client, string][]
              ).map(([value, label]) => (
                <button
                  key={value}
                  type="button"
                  onClick={() => setClient(value)}
                  className={cx(
                    "flex-1 rounded px-3 py-1.5 text-sm font-medium transition-colors",
                    client === value
                      ? "bg-white text-slate-950 shadow-sm dark:bg-slate-700 dark:text-white"
                      : "text-slate-600 hover:text-slate-900 dark:text-slate-400 dark:hover:text-white",
                  )}
                >
                  {label}
                </button>
              ))}
            </div>

            <div className="grid gap-3 sm:grid-cols-2">
              <InputFieldWithLabel
                size="SM"
                label={m.mcp_config_server_name()}
                value={serverName}
                maxLength={40}
                onChange={e => setServerName(e.target.value)}
              />
              <InputFieldWithLabel
                size="SM"
                label={m.mcp_config_token()}
                placeholder="jkvm_…"
                value={tokenInput ?? initialToken}
                onChange={e => setTokenInput(e.target.value)}
              />
            </div>
            {!token && (
              <p className="flex items-start gap-1.5 text-xs text-amber-700 dark:text-amber-400">
                <LuTriangleAlert className="mt-0.5 h-3.5 w-3.5 shrink-0" />
                <span>{m.mcp_config_no_token()}</span>
              </p>
            )}

            {snippets.map(snippet => (
              <div key={snippet.title} className="space-y-1">
                <div className="flex items-center justify-between gap-2">
                  <p className="text-sm font-medium">{snippet.title}</p>
                  <Button
                    size="XS"
                    theme="light"
                    LeadingIcon={LuCopy}
                    text={m.mcp_copy()}
                    onClick={() => copy(snippet.text)}
                  />
                </div>
                <pre className="overflow-x-auto rounded bg-slate-100 px-3 py-2 font-mono text-xs whitespace-pre select-all dark:bg-slate-950">
                  {snippet.text}
                </pre>
              </div>
            ))}

            <p className="text-xs text-slate-600 dark:text-slate-400">
              {m.mcp_config_footer_kvm()}
            </p>
            <div className="flex justify-end">
              <CloseButton as={Button} size="SM" theme="light" text={m.close()} />
            </div>
          </div>
        </div>
      </div>
    </Modal>
  );
}
