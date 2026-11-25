import { ReactNode, useState } from "react";
import { CloseButton } from "@headlessui/react";
import { LuCopy, LuExternalLink } from "react-icons/lu";

import { cx } from "@/cva.config";
import { Button } from "@components/Button";
import { InputFieldWithLabel } from "@components/InputField";
import Modal from "@components/Modal";
import { useCopyToClipboard } from "@components/useCopyToClipBoard";
import notifications from "@/notifications";
import { m } from "@localizations/messages.js";

export type System = "windows" | "macos" | "linux";

interface Arch {
  id: string;
  label: string;
  // The release file, for a version or "<version>".
  file: (version: string) => string;
  // Linux: the AppImage's architecture suffix and what `uname -m` prints.
  suffix?: string;
  machine?: string;
}

// The packages the OS Services release step builds (xtask package): one
// Windows installer, a DMG for each Mac architecture, an AppImage for each
// Linux one.
const SYSTEMS: { id: System; label: string; arches: Arch[] }[] = [
  {
    id: "windows",
    label: "Windows",
    arches: [{ id: "x64", label: "x64", file: v => `IP-KVM OS Services_${v}_x64-setup.exe` }],
  },
  {
    id: "macos",
    label: "macOS",
    arches: [
      {
        id: "aarch64",
        label: "Apple silicon",
        file: v => `IP-KVM OS Services_${v}_aarch64.dmg`,
      },
      { id: "x64", label: "Intel", file: v => `IP-KVM OS Services_${v}_x64.dmg` },
    ],
  },
  {
    id: "linux",
    label: "Linux",
    arches: [
      {
        id: "amd64",
        label: "x86-64 (amd64)",
        file: v => `IP-KVM-OS-Services_${v}_amd64.AppImage`,
        suffix: "amd64",
        machine: "x86_64",
      },
      {
        id: "arm64",
        label: "ARM 64-bit (arm64)",
        file: v => `IP-KVM-OS-Services_${v}_arm64.AppImage`,
        suffix: "arm64",
        machine: "aarch64",
      },
      {
        id: "armhf",
        label: "ARM 32-bit (armhf)",
        file: v => `IP-KVM-OS-Services_${v}_armhf.AppImage`,
        suffix: "armhf",
        machine: "armv7l",
      },
    ],
  },
];

const APPRUN = "~/.local/share/ipkvm-os-services/app/AppRun";

// The OS Services interface (class ff, subclass 4a, protocol 01), as the
// packaged 70-ipkvm-os-services.rules matches it.
const USB_MATCH = `SUBSYSTEM=="usb", ENV{DEVTYPE}=="usb_device", ENV{ID_USB_INTERFACES}=="*:ff4a01:*"`;

// A value as one shell word.
function shellWord(value: string) {
  return /^[\w@%+=:,./~-]+$/.test(value) ? value : `'${value.replaceAll("'", `'\\''`)}'`;
}

function Tabs<T extends string>({
  value,
  options,
  onChange,
  small,
}: {
  value: T;
  options: [T, string][];
  onChange: (value: T) => void;
  small?: boolean;
}) {
  return (
    <div
      role="tablist"
      className={cx(
        "flex flex-wrap gap-1 rounded-md p-1",
        small ? "bg-slate-50 dark:bg-slate-800/60" : "bg-slate-100 dark:bg-slate-800",
      )}
    >
      {options.map(([option, label]) => (
        <button
          key={option}
          type="button"
          role="tab"
          aria-selected={value === option}
          onClick={() => onChange(option)}
          className={cx(
            "flex-1 rounded px-3 font-medium whitespace-nowrap transition-colors",
            small ? "py-1 text-xs" : "py-1.5 text-sm",
            value === option
              ? "bg-white text-slate-950 shadow-sm dark:bg-slate-700 dark:text-white"
              : "text-slate-600 hover:text-slate-900 dark:text-slate-400 dark:hover:text-white",
          )}
        >
          {label}
        </button>
      ))}
    </div>
  );
}

function Command({ text }: { text: string }) {
  const { copy } = useCopyToClipboard();
  return (
    <div className="relative mt-1.5">
      <pre className="overflow-x-auto rounded bg-slate-100 py-2 pr-20 pl-3 font-mono text-xs whitespace-pre select-all dark:bg-slate-950">
        {text}
      </pre>
      <div className="absolute top-1 right-1">
        <Button
          size="XS"
          theme="light"
          LeadingIcon={LuCopy}
          text={m.mcp_copy()}
          onClick={async () => {
            if (await copy(text)) notifications.success(m.os_services_guide_copied());
          }}
        />
      </div>
    </div>
  );
}

function Section({ title, children }: { title: string; children: ReactNode }) {
  return (
    <div className="space-y-1.5">
      <h3 className="text-sm font-semibold text-slate-950 dark:text-white">{title}</h3>
      <div className="space-y-2 text-sm text-slate-700 dark:text-slate-300">{children}</div>
    </div>
  );
}

function Steps({ steps }: { steps: { text: string; command?: string }[] }) {
  return (
    <ol className="list-decimal space-y-2 pl-5 marker:text-slate-500">
      {steps.map(step => (
        <li key={step.text}>
          {step.text}
          {step.command && <Command text={step.command} />}
        </li>
      ))}
    </ol>
  );
}

function Notes({ notes }: { notes: { text: string; command?: string }[] }) {
  return (
    <ul className="list-disc space-y-2 pl-5 marker:text-slate-500">
      {notes.map(note => (
        <li key={note.text}>
          {note.text}
          {note.command && <Command text={note.command} />}
        </li>
      ))}
    </ul>
  );
}

function WindowsGuide() {
  return (
    <Section title={m.os_services_guide_install()}>
      <Steps
        steps={[
          { text: m.os_services_guide_windows_step_run() },
          { text: m.os_services_guide_windows_step_signin() },
        ]}
      />
    </Section>
  );
}

function MacGuide() {
  return (
    <Section title={m.os_services_guide_install()}>
      <Steps
        steps={[
          { text: m.os_services_guide_macos_step_drag() },
          { text: m.os_services_guide_macos_step_open() },
          { text: m.os_services_guide_macos_step_recording() },
        ]}
      />
    </Section>
  );
}

function LinuxGuide({ arch, fingerprint }: { arch: Arch; fingerprint: string }) {
  const [user, setUser] = useState("");
  const name = user.trim() || "<user>";
  const appImage = `./IP-KVM-OS-Services_*_${arch.suffix}.AppImage`;
  const x86 = arch.suffix === "amd64";
  return (
    <>
      <Section title={m.os_services_guide_install()}>
        <Steps
          steps={[
            {
              text: m.os_services_guide_linux_step_run(),
              command: `chmod +x ${appImage}\n${appImage}`,
            },
            { text: m.os_services_guide_linux_step_usb() },
            { text: m.os_services_guide_linux_step_signout() },
            { text: m.os_services_guide_linux_step_installed() },
          ]}
        />
        <p className="text-xs text-slate-600 dark:text-slate-400">
          {m.os_services_guide_linux_no_dialogs()}
        </p>
        <Command
          text={[
            `sudo ${APPRUN} --install-linux-usb-access`,
            `sudo ${APPRUN} --install-linux-capture-access`,
            `sudo usermod -aG ipkvm-capture $USER`,
          ].join("\n")}
        />
      </Section>

      <Section title={m.os_services_guide_requirements()}>
        <Notes
          notes={[
            { text: m.os_services_guide_linux_glibc() },
            x86
              ? {
                  text: m.os_services_guide_linux_video_x86(),
                  command: "sudo apt install libva2 libva-drm2 libegl1 libgles2",
                }
              : { text: m.os_services_guide_linux_video_arm() },
            { text: m.os_services_guide_linux_fuse() },
          ]}
        />
      </Section>

      <Section title={m.os_services_guide_headless_title()}>
        <p>{m.os_services_guide_headless_intro()}</p>
        <InputFieldWithLabel
          size="SM"
          label={m.os_services_guide_headless_user()}
          placeholder="user"
          value={user}
          maxLength={32}
          onChange={event => setUser(event.target.value)}
        />
        <Steps
          steps={[
            {
              text: m.os_services_guide_headless_step_linger(),
              command: `loginctl enable-linger ${shellWord(name)}`,
            },
            {
              text: m.os_services_guide_headless_step_run(),
              command: [
                `su - ${shellWord(name)}`,
                "export XDG_RUNTIME_DIR=/run/user/$(id -u)",
                "export DBUS_SESSION_BUS_ADDRESS=unix:path=$XDG_RUNTIME_DIR/bus",
                `chmod +x ${appImage}`,
                appImage,
              ].join("\n"),
            },
            {
              text: m.os_services_guide_headless_step_usb(),
              command: [
                `/home/${name}/.local/share/ipkvm-os-services/app/AppRun --install-linux-usb-access`,
                `echo ${shellWord(`${USB_MATCH}, OWNER="${name}", MODE="0600"`)} > /etc/udev/rules.d/71-ipkvm-os-services-${name}.rules`,
                "udevadm control --reload-rules",
                "udevadm trigger --subsystem-match=usb",
              ].join("\n"),
            },
            {
              text: m.os_services_guide_headless_step_boot(),
              command: [
                "systemctl --user add-wants default.target ipkvm-os-services.service",
                "systemctl --user restart ipkvm-os-services.service",
              ].join("\n"),
            },
            {
              text: m.os_services_guide_headless_step_allow(),
              command: `${APPRUN} --approve-kvm ${fingerprint || "<fingerprint>"}`,
            },
          ]}
        />
        <p className="text-xs text-slate-600 dark:text-slate-400">
          {m.os_services_guide_headless_limits()}
        </p>
      </Section>
    </>
  );
}

// The page that shows the guide on its own, in a new tab. The query carries
// what the dialog knows and the tabs it had open.
export function osServicesGuideHref(state: {
  version?: string;
  fingerprint: string;
  system: System;
  arch: string;
}) {
  const query = new URLSearchParams({ system: state.system, arch: state.arch });
  if (state.version) query.set("version", state.version);
  if (state.fingerprint) query.set("fingerprint", state.fingerprint);
  return `${import.meta.env.BASE_URL.replace(/\/$/, "")}/os-services-guide?${query}`;
}

// The install steps for each operating system and architecture: tabs for
// the system, then for its architecture. The text follows the os-services
// repository's docs/install.md; the Linux part without a desktop follows
// what a headless single-board computer needed.
export function OsServicesGuide({
  version,
  fingerprint,
  initialSystem,
  initialArch,
  newTab,
}: {
  // The release version for the file names, if the KVM knows it.
  version?: string;
  // This KVM's key as the approval dialog shows it; empty until the first
  // sender connects.
  fingerprint: string;
  initialSystem?: string;
  initialArch?: string;
  // Shows the button that opens the guide in a new tab.
  newTab?: boolean;
}) {
  const [systemId, setSystemId] = useState<System>(
    SYSTEMS.find(entry => entry.id === initialSystem)?.id ?? "windows",
  );
  const [archIds, setArchIds] = useState<Partial<Record<System, string>>>(
    initialArch ? { [systemId]: initialArch } : {},
  );
  const system = SYSTEMS.find(entry => entry.id === systemId)!;
  const arch = system.arches.find(entry => entry.id === archIds[systemId]) ?? system.arches[0];
  const file = arch.file(version || "<version>");
  const which = {
    windows: m.os_services_guide_which_windows(),
    macos: m.os_services_guide_which_macos(),
    linux: m.os_services_guide_which_linux({ machine: arch.machine ?? "" }),
  }[systemId];

  return (
    <div className="space-y-4 text-slate-900 dark:text-slate-100">
      <div className="flex items-start justify-between gap-4">
        <div className="space-y-1">
          <h2 className="font-semibold text-slate-950 dark:text-white">
            {m.os_services_guide_title()}
          </h2>
          <p className="text-sm text-slate-700 dark:text-slate-300">
            {m.os_services_guide_intro()}
          </p>
        </div>
        {newTab && (
          <Button
            size="XS"
            theme="light"
            LeadingIcon={LuExternalLink}
            text={m.os_services_guide_new_tab()}
            onClick={() =>
              window.open(
                osServicesGuideHref({ version, fingerprint, system: systemId, arch: arch.id }),
                "_blank",
                "noopener",
              )
            }
            data-testid="os-services-guide-new-tab"
          />
        )}
      </div>

      <Tabs
        value={systemId}
        options={SYSTEMS.map(entry => [entry.id, entry.label])}
        onChange={setSystemId}
      />
      {system.arches.length > 1 && (
        <Tabs
          small
          value={arch.id}
          options={system.arches.map(entry => [entry.id, entry.label])}
          onChange={id => setArchIds(ids => ({ ...ids, [systemId]: id }))}
        />
      )}

      <Section title={m.os_services_guide_file()}>
        <p className="font-mono text-xs break-all text-slate-950 dark:text-white">{file}</p>
        <p>{m.os_services_guide_download()}</p>
        <p className="text-xs text-slate-600 dark:text-slate-400">{which}</p>
      </Section>

      {systemId === "windows" && <WindowsGuide />}
      {systemId === "macos" && <MacGuide />}
      {systemId === "linux" && <LinuxGuide arch={arch} fingerprint={fingerprint} />}

      <Section title={m.os_services_guide_allow_title()}>
        <p>{m.os_services_guide_allow_description()}</p>
        <p className="font-mono text-xs break-all text-slate-950 dark:text-white">
          {fingerprint || m.os_services_guide_allow_no_fingerprint()}
        </p>
      </Section>
    </div>
  );
}

// The guide in a dialog, opened from the OS Services settings.
export default function OsServicesGuideDialog({
  open,
  onClose,
  version,
  fingerprint,
}: {
  open: boolean;
  onClose: () => void;
  version?: string;
  fingerprint: string;
}) {
  return (
    <Modal open={open} onClose={onClose}>
      <div className="mx-auto max-w-2xl px-4">
        <div className="pointer-events-auto relative w-full overflow-hidden rounded-lg border border-slate-200 bg-white shadow-sm dark:border-slate-800 dark:bg-slate-900">
          <div className="max-h-[85vh] space-y-4 overflow-y-auto p-6">
            <OsServicesGuide version={version} fingerprint={fingerprint} newTab />
            <div className="flex justify-end">
              <CloseButton as={Button} size="SM" theme="light" text={m.close()} />
            </div>
          </div>
        </div>
      </div>
    </Modal>
  );
}
