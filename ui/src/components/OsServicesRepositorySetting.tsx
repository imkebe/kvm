import { useEffect, useState } from "react";

import { Button } from "@components/Button";
import { InputFieldWithLabel } from "@components/InputField";
import { SelectMenuBasic } from "@components/SelectMenuBasic";
import { SettingsItem } from "@components/SettingsItem";
import { m } from "@localizations/messages.js";
import {
  OFFICIAL_OS_SERVICES_REPOSITORY,
  OsServicesRepositoryKind,
  osServicesRepositoryFile,
  osServicesRepositoryHttpAllowed,
  osServicesRepositoryKind,
} from "@/utils/osServicesRepository";

// Where OS Services releases come from: the offline installer disc the KVM
// downloads and mounts, the installer disc's download link, and updates.
export default function OsServicesRepositorySetting({
  repository,
  disabled,
  onSave,
}: {
  // The saved URL; empty or absent for JetKVM's server.
  repository?: string;
  disabled?: boolean;
  onSave: (repository: string) => void;
}) {
  const savedKind = osServicesRepositoryKind(repository);
  const [kind, setKind] = useState<OsServicesRepositoryKind>(savedKind);
  const [draft, setDraft] = useState(savedKind === "official" ? "" : (repository ?? ""));

  // Follow a change saved elsewhere.
  useEffect(() => {
    const next = osServicesRepositoryKind(repository);
    setKind(next);
    setDraft(next === "official" ? "" : (repository ?? ""));
  }, [repository]);

  const choose = (next: OsServicesRepositoryKind) => {
    setKind(next);
    if (next === "official") {
      if (savedKind !== "official") onSave("");
      return;
    }
    if (next !== savedKind) setDraft(next === "github" ? "https://github.com/" : "https://");
  };

  const trimmed = draft.trim().replace(/\/+$/, "");
  const valid =
    kind === "github"
      ? /^https:\/\/(www\.)?github\.com\/[^/\s]+\/[^/\s]+$/i.test(trimmed)
      : /^https?:\/\/[^\s/?#]+(\/[^\s?#]*)?$/i.test(trimmed);
  const httpAllowed = kind !== "custom" || osServicesRepositoryHttpAllowed(trimmed);
  const changed = kind !== "official" && trimmed !== (repository ?? "");
  const shown = kind === "official" ? OFFICIAL_OS_SERVICES_REPOSITORY : trimmed;

  return (
    <div className="space-y-3">
      <SettingsItem
        title={m.os_services_repository_title()}
        description={m.os_services_repository_description()}
      >
        <SelectMenuBasic
          size="SM"
          value={kind}
          disabled={disabled}
          onChange={event => choose(event.target.value as OsServicesRepositoryKind)}
          options={[
            { value: "official", label: m.os_services_repository_official() },
            { value: "github", label: m.os_services_repository_github() },
            { value: "custom", label: m.os_services_repository_custom() },
          ]}
        />
      </SettingsItem>
      <div className="ml-2 space-y-2 border-l-2 border-slate-200 pl-4 dark:border-slate-700">
        {kind !== "official" && (
          <div className="flex items-end gap-2">
            <div className="flex-1">
              <InputFieldWithLabel
                size="SM"
                label={
                  kind === "github"
                    ? m.os_services_repository_github_label()
                    : m.os_services_repository_custom_label()
                }
                placeholder={
                  kind === "github"
                    ? "https://github.com/owner/os-services"
                    : "https://example.com/os-services"
                }
                value={draft}
                disabled={disabled}
                onChange={event => setDraft(event.target.value)}
                onKeyDown={event => {
                  event.stopPropagation();
                  if (event.key === "Enter" && valid && httpAllowed && changed) onSave(trimmed);
                }}
                onKeyUp={event => event.stopPropagation()}
              />
            </div>
            <Button
              size="SM"
              theme="primary"
              text={m.os_services_repository_save()}
              disabled={disabled || !valid || !httpAllowed || !changed}
              onClick={() => onSave(trimmed)}
            />
          </div>
        )}
        {valid && !httpAllowed && (
          <p className="text-xs text-red-600 dark:text-red-400">
            {m.os_services_repository_http_private()}
          </p>
        )}
        <p className="text-xs text-slate-600 dark:text-slate-400">
          {kind === "github"
            ? m.os_services_repository_github_help()
            : kind === "custom"
              ? m.os_services_repository_custom_help()
              : m.os_services_repository_official_help()}
        </p>
        {(kind === "official" || valid) && (
          <p className="truncate font-mono text-[11px] text-slate-500 dark:text-slate-400">
            {m.os_services_repository_example()}{" "}
            {osServicesRepositoryFile(shown, "jetkvm-os-services.iso")}
          </p>
        )}
        {kind !== "official" && (
          <p className="text-xs text-amber-700 dark:text-amber-400">
            {m.os_services_repository_signature_note()}
          </p>
        )}
      </div>
    </div>
  );
}
