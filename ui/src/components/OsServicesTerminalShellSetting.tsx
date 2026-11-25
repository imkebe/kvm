import { useEffect, useState } from "react";

import { Button } from "@components/Button";
import { InputFieldWithLabel } from "@components/InputField";
import { SelectMenuBasic } from "@components/SelectMenuBasic";
import { SettingsItem } from "@components/SettingsItem";
import { validCommand } from "@components/OsServicesTerminalMuxSetting";
import { m } from "@localizations/messages.js";

// What getOsServicesTerminalShell reports: the shell the KVM writes the
// typed commands for, and why.
export interface TerminalShellChoice {
  kind: TerminalShellKind;
  source: "setting" | "command" | "os" | "unknown";
  os?: string;
}

export type TerminalShellKind = "posix" | "fish" | "pwsh" | "powershell" | "cmd";

export const terminalShellLabel: Record<TerminalShellKind, () => string> = {
  posix: m.os_services_terminal_shell_posix,
  fish: m.os_services_terminal_shell_fish,
  pwsh: m.os_services_terminal_shell_pwsh,
  powershell: m.os_services_terminal_shell_powershell,
  cmd: m.os_services_terminal_shell_cmd,
};

export function terminalShellReason(choice: TerminalShellChoice) {
  const name = terminalShellLabel[choice.kind]();
  switch (choice.source) {
    case "setting":
      return m.os_services_terminal_shell_reason_setting({ name });
    case "command":
      return m.os_services_terminal_shell_reason_command({ name });
    case "os":
      return m.os_services_terminal_shell_reason_os({ name, os: choice.os ?? "" });
    default:
      return m.os_services_terminal_shell_reason_unknown({ name });
  }
}

// Under Terminal access: which shell the browser terminal runs (detected
// or set), and a custom command that starts another shell in it.
export default function OsServicesTerminalShellSetting({
  shell,
  command,
  choice,
  disabled,
  onSave,
}: {
  shell?: string;
  command?: string;
  choice: TerminalShellChoice | null;
  disabled?: boolean;
  onSave: (changes: { terminal_shell?: string; terminal_shell_command?: string }) => void;
}) {
  const savedCommand = command ?? "";
  const [draft, setDraft] = useState(savedCommand);
  useEffect(() => setDraft(savedCommand), [savedCommand]);

  const trimmed = draft.trim();
  const valid = validCommand(trimmed);
  const changed = trimmed !== savedCommand;

  const autoLabel =
    choice && (choice.source === "os" || choice.source === "command" || choice.source === "unknown")
      ? m.os_services_terminal_shell_auto_detected({ name: terminalShellLabel[choice.kind]() })
      : m.os_services_terminal_shell_auto();

  return (
    <div className="ml-2 space-y-3 border-l-2 border-slate-200 pl-4 dark:border-slate-700">
      <SettingsItem
        size="SM"
        title={m.os_services_terminal_shell_title()}
        description={
          choice ? terminalShellReason(choice) : m.os_services_terminal_shell_description()
        }
      >
        <SelectMenuBasic
          size="SM"
          label=""
          value={shell || "auto"}
          disabled={disabled}
          onChange={event => onSave({ terminal_shell: event.target.value })}
          options={[
            { value: "auto", label: autoLabel },
            ...(Object.keys(terminalShellLabel) as TerminalShellKind[]).map(kind => ({
              value: kind,
              label: terminalShellLabel[kind](),
            })),
          ]}
        />
      </SettingsItem>
      <div className="flex items-end gap-2">
        <div className="flex-1">
          <InputFieldWithLabel
            size="SM"
            label={m.os_services_terminal_shell_command_label()}
            placeholder="pwsh -NoLogo"
            value={draft}
            disabled={disabled}
            maxLength={256}
            className="font-mono"
            onChange={event => setDraft(event.target.value)}
            onKeyDown={event => {
              event.stopPropagation();
              if (event.key === "Enter" && valid && changed)
                onSave({ terminal_shell_command: trimmed });
            }}
            onKeyUp={event => event.stopPropagation()}
          />
        </div>
        <Button
          size="SM"
          theme="primary"
          text={m.os_services_terminal_mux_save()}
          disabled={disabled || !valid || !changed}
          onClick={() => onSave({ terminal_shell_command: trimmed })}
        />
      </div>
      <p className="text-xs text-slate-600 dark:text-slate-400">
        {m.os_services_terminal_shell_command_help()}
      </p>
    </div>
  );
}
