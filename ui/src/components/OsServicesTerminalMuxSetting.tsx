import { useEffect, useState } from "react";
import { LuCircleHelp } from "react-icons/lu";

import { Button } from "@components/Button";
import { Checkbox } from "@components/Checkbox";
import { InputFieldWithLabel } from "@components/InputField";
import { SelectMenuBasic } from "@components/SelectMenuBasic";
import { SettingsItem } from "@components/SettingsItem";
import { cx } from "@/cva.config";
import {
  TerminalShellChoice,
  TerminalShellKind,
  terminalShellLabel,
  terminalShellReason,
} from "@components/OsServicesTerminalShellSetting";
import { m } from "@localizations/messages.js";

// What each macro becomes in each shell; the KVM expands them
// (os_services_terminal_shell.go, terminalMacros). Windows PowerShell 5.1
// has no || or &&: there %OR% and %AND% open a block that the KVM closes at
// the end of the line.
const shellKinds: TerminalShellKind[] = ["posix", "fish", "pwsh", "powershell", "cmd"];
const macros: { name: string; meaning: () => string; forms: Record<TerminalShellKind, string> }[] =
  [
    {
      name: "%OR%",
      meaning: m.os_services_terminal_macro_or,
      forms: {
        posix: "||",
        fish: "; or",
        pwsh: "||",
        powershell: "; if (-not $?) { … }",
        cmd: "||",
      },
    },
    {
      name: "%AND%",
      meaning: m.os_services_terminal_macro_and,
      forms: { posix: "&&", fish: "; and", pwsh: "&&", powershell: "; if ($?) { … }", cmd: "&&" },
    },
    {
      name: "%SEP%",
      meaning: m.os_services_terminal_macro_sep,
      forms: { posix: ";", fish: ";", pwsh: ";", powershell: ";", cmd: "&" },
    },
    {
      name: "%QUIET%",
      meaning: m.os_services_terminal_macro_quiet,
      forms: {
        posix: "2>/dev/null",
        fish: "2>/dev/null",
        pwsh: "2>$null",
        powershell: "2>$null",
        cmd: "2>NUL",
      },
    },
    {
      name: "%NULL%",
      meaning: m.os_services_terminal_macro_null,
      forms: {
        posix: "/dev/null",
        fish: "/dev/null",
        pwsh: "$null",
        powershell: "$null",
        cmd: "NUL",
      },
    },
    {
      name: "%HOME%",
      meaning: m.os_services_terminal_macro_home,
      forms: {
        posix: "$HOME",
        fish: "$HOME",
        pwsh: "$HOME",
        powershell: "$HOME",
        cmd: "%USERPROFILE%",
      },
    },
  ];

function MacroReference({ choice }: { choice: TerminalShellChoice | null }) {
  return (
    <div className="space-y-2 rounded-md border border-slate-800/10 p-3 text-xs dark:border-slate-300/20">
      <p className="text-slate-700 dark:text-slate-300">{m.os_services_terminal_macro_intro()}</p>
      {choice && (
        <p className="font-medium text-slate-800 dark:text-slate-200">
          {terminalShellReason(choice)}
        </p>
      )}
      <div className="overflow-x-auto">
        <table className="w-full border-collapse text-left">
          <thead>
            <tr className="text-slate-500 dark:text-slate-400">
              <th className="py-1 pr-3 font-medium">{m.os_services_terminal_macro_name()}</th>
              <th className="py-1 pr-3 font-medium">{m.os_services_terminal_macro_meaning()}</th>
              {shellKinds.map(kind => (
                <th
                  key={kind}
                  className={cx("py-1 pr-3 font-medium whitespace-nowrap", {
                    "text-blue-700 dark:text-blue-300": choice?.kind === kind,
                  })}
                >
                  {terminalShellLabel[kind]()}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {macros.map(macro => (
              <tr
                key={macro.name}
                className="border-t border-slate-800/10 dark:border-slate-300/20"
              >
                <td className="py-1 pr-3 font-mono whitespace-nowrap">{macro.name}</td>
                <td className="py-1 pr-3 text-slate-700 dark:text-slate-300">{macro.meaning()}</td>
                {shellKinds.map(kind => (
                  <td
                    key={kind}
                    className={cx("py-1 pr-3 font-mono whitespace-nowrap", {
                      "font-semibold text-blue-700 dark:text-blue-300": choice?.kind === kind,
                    })}
                  >
                    {macro.forms[kind]}
                  </td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <p className="text-slate-600 dark:text-slate-400">{m.os_services_terminal_macro_example()}</p>
    </div>
  );
}

// Known multiplexers, each with its own start (create the session) and
// restore (reattach to it) command. Choosing one fills both fields, which
// stay editable. Rex documents no command that attaches a plain terminal to
// a session (superlogical.com/rex/docs/reference/cli), so its restore is
// left for the user.
const presets = [
  { value: "tmux", start: "tmux new-session -s jetkvm", restore: "tmux attach-session -t jetkvm" },
  { value: "screen", start: "screen -S jetkvm", restore: "screen -D -r jetkvm" },
  { value: "zellij", start: "zellij --session jetkvm", restore: "zellij attach jetkvm" },
  { value: "tuios", start: "tuios new jetkvm", restore: "tuios attach jetkvm" },
  { value: "rex", start: "rex new jetkvm", restore: "" },
] as const;

const presetLabel: Record<(typeof presets)[number]["value"], () => string> = {
  tmux: m.os_services_terminal_mux_tmux,
  screen: m.os_services_terminal_mux_screen,
  zellij: m.os_services_terminal_mux_zellij,
  tuios: m.os_services_terminal_mux_tuios,
  rex: m.os_services_terminal_mux_rex,
};

// The KVM takes one line of printable text, at most 256 characters.
// eslint-disable-next-line no-control-regex
export const validCommand = (text: string) => text.length <= 256 && !/[\x00-\x1f\x7f]/.test(text);

// The terminal multiplexer of the browser terminal on the target: a switch
// and two commands (OsServicesSettings.terminal_mux_*). The KVM types
// "restore || start" into each new browser terminal.
export default function OsServicesTerminalMuxSetting({
  enabled,
  start,
  restore,
  choice,
  disabled,
  onSave,
}: {
  enabled: boolean;
  choice: TerminalShellChoice | null;
  start?: string;
  restore?: string;
  disabled?: boolean;
  onSave: (changes: {
    terminal_mux_enabled?: boolean;
    terminal_mux_start?: string;
    terminal_mux_restore?: string;
  }) => void;
}) {
  const [helpOpen, setHelpOpen] = useState(false);
  const savedStart = start ?? "";
  const savedRestore = restore ?? "";
  const [draftStart, setDraftStart] = useState(savedStart);
  const [draftRestore, setDraftRestore] = useState(savedRestore);

  // Follow a change saved elsewhere.
  useEffect(() => setDraftStart(savedStart), [savedStart]);
  useEffect(() => setDraftRestore(savedRestore), [savedRestore]);

  const trimmedStart = draftStart.trim();
  const trimmedRestore = draftRestore.trim();
  const valid =
    validCommand(trimmedStart) &&
    validCommand(trimmedRestore) &&
    (trimmedStart !== "" || trimmedRestore !== "");
  const changed = trimmedStart !== savedStart || trimmedRestore !== savedRestore;
  const save = () =>
    onSave({ terminal_mux_start: trimmedStart, terminal_mux_restore: trimmedRestore });

  const matching = presets.find(p => p.start === trimmedStart && p.restore === trimmedRestore);

  const field = (
    label: string,
    value: string,
    set: (value: string) => void,
    placeholder: string,
  ) => (
    <InputFieldWithLabel
      size="SM"
      label={label}
      placeholder={placeholder}
      value={value}
      disabled={disabled}
      maxLength={256}
      className="font-mono"
      onChange={event => set(event.target.value)}
      onKeyDown={event => {
        event.stopPropagation();
        if (event.key === "Enter" && valid && changed) save();
      }}
      onKeyUp={event => event.stopPropagation()}
    />
  );

  return (
    <div className="space-y-3">
      <SettingsItem
        title={m.os_services_terminal_mux_title()}
        description={m.os_services_terminal_mux_description()}
      >
        <Checkbox
          checked={enabled}
          disabled={disabled}
          data-testid="os-services-terminal-mux-enabled"
          onChange={event => onSave({ terminal_mux_enabled: event.target.checked })}
        />
      </SettingsItem>
      {enabled && (
        <div className="ml-2 space-y-3 border-l-2 border-slate-200 pl-4 dark:border-slate-700">
          <SettingsItem
            size="SM"
            title={m.os_services_terminal_mux_preset_title()}
            description={m.os_services_terminal_mux_preset_description()}
          >
            <SelectMenuBasic
              size="SM"
              label=""
              value={matching?.value ?? "custom"}
              disabled={disabled}
              onChange={event => {
                const preset = presets.find(p => p.value === event.target.value);
                if (!preset) return;
                setDraftStart(preset.start);
                setDraftRestore(preset.restore);
              }}
              options={[
                ...presets.map(p => ({ value: p.value, label: presetLabel[p.value]() })),
                { value: "custom", label: m.os_services_terminal_mux_custom() },
              ]}
            />
          </SettingsItem>
          {field(
            m.os_services_terminal_mux_start_label(),
            draftStart,
            setDraftStart,
            "tmux new-session -s work",
          )}
          {field(
            m.os_services_terminal_mux_restore_label(),
            draftRestore,
            setDraftRestore,
            "tmux attach-session -t work",
          )}
          <div className="flex items-center gap-2">
            <Button
              size="SM"
              theme="primary"
              text={m.os_services_terminal_mux_save()}
              disabled={disabled || !valid || !changed}
              onClick={save}
            />
            <Button
              size="SM"
              theme="light"
              LeadingIcon={LuCircleHelp}
              text={m.os_services_terminal_macro_button()}
              aria-expanded={helpOpen}
              onClick={() => setHelpOpen(open => !open)}
            />
            {changed && (
              <span className="text-xs text-amber-700 dark:text-amber-300">
                {m.os_services_terminal_mux_unsaved()}
              </span>
            )}
          </div>
          {helpOpen && <MacroReference choice={choice} />}
          <p className="text-xs text-slate-600 dark:text-slate-400">
            {matching?.value === "rex"
              ? m.os_services_terminal_mux_rex_note()
              : m.os_services_terminal_mux_help()}
          </p>
        </div>
      )}
    </div>
  );
}
