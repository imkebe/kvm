import { useCallback, useEffect, useMemo, useState } from "react";

import { JsonRpcResponse, useJsonRpc } from "@hooks/useJsonRpc";
import { useCapability, useUiStore } from "@hooks/stores";
import { m } from "@localizations/messages.js";
import { SettingsItem } from "@components/SettingsItem";
import Checkbox from "@components/Checkbox";
import { Button } from "@components/Button";
import { SelectMenuBasic } from "@components/SelectMenuBasic";
import { SettingsSectionHeader } from "@components/SettingsSectionHeader";
import Fieldset from "@components/Fieldset";
import { cx } from "@/cva.config";
import notifications from "@/notifications";
import { sleep } from "@/utils";

export interface USBConfig {
  vendor_id: string;
  product_id: string;
  serial_number: string;
  manufacturer: string;
  product: string;
}

export interface UsbDeviceConfig {
  keyboard: boolean;
  absolute_mouse: boolean;
  relative_mouse: boolean;
  mass_storage: boolean;
  serial_console: boolean;
  audio: boolean;
}

type FeatureKey = keyof UsbDeviceConfig;
// Rows of the list that cannot be switched on this page: the always-present
// USB wake-up function and OS Services (switched on its own page).
type FixedRow = "wake" | "os_services";
const isFixedRow = (key: string): key is FixedRow => key === "wake" || key === "os_services";

const none: UsbDeviceConfig = {
  keyboard: false,
  absolute_mouse: false,
  relative_mouse: false,
  mass_storage: false,
  serial_console: false,
  audio: false,
};
const hid = { keyboard: true, absolute_mouse: true, relative_mouse: true };
const defaultUsbDeviceConfig: UsbDeviceConfig = {
  ...none,
  ...hid,
  mass_storage: true,
  audio: true,
};

interface UsbPreset {
  label: string;
  value: string;
  description?: string;
  config?: UsbDeviceConfig;
}

// Class presets. Media input sends media to the target (camera,
// microphone); media output brings the target's sound to the browser.
const usbPresets: UsbPreset[] = [
  {
    label: m.usb_device_keyboard_mouse_mass_storage_and_audio(),
    value: "default",
    config: defaultUsbDeviceConfig,
  },
  {
    label: m.usb_preset_hid(),
    value: "hid",
    description: m.usb_preset_hid_description(),
    config: { ...none, ...hid },
  },
  {
    label: m.usb_device_keyboard_only(),
    value: "keyboard_only",
    config: { ...none, keyboard: true },
  },
  {
    label: m.usb_preset_storage(),
    value: "storage",
    description: m.usb_preset_storage_description(),
    config: { ...none, ...hid, mass_storage: true },
  },
  {
    label: m.usb_preset_media_output(),
    value: "media_output",
    description: m.usb_preset_media_output_description(),
    config: { ...none, ...hid, audio: true },
  },
  { label: m.usb_device_custom(), value: "custom" },
];

interface EndpointUsage {
  total: number;
  used: number;
  items: { name: string; endpoints: number }[];
}

function endpointLabel(name: string) {
  switch (name) {
    case "keyboard":
      return m.usb_endpoint_keyboard();
    case "absolute_mouse":
      return m.usb_endpoint_absolute_mouse();
    case "relative_mouse":
      return m.usb_endpoint_relative_mouse();
    case "wake":
      return m.usb_endpoint_wake();
    case "mass_storage":
      return m.usb_endpoint_mass_storage();
    case "serial_console":
      return m.usb_endpoint_serial_console();
    case "os_services":
      return m.usb_endpoint_os_services();
    case "spare":
      return m.usb_endpoint_spare();
    default:
      return name;
  }
}

// The endpoint group (getUsbEndpointUsage item name) a feature takes; the
// target's audio (speaker direction) takes no IN endpoint.
const featureGroup: Record<FeatureKey, string | null> = {
  keyboard: "keyboard",
  absolute_mouse: "absolute_mouse",
  relative_mouse: "relative_mouse",
  mass_storage: "mass_storage",
  serial_console: "serial_console",
  audio: null,
};

type Cell = { group: string; kind: "used" | "added" };

// Reserved endpoints (the planner's spare functions on IN 6 and IN 8) are
// striped grey and green: taken, but carrying no data.
const reservedStyle = {
  backgroundImage:
    "repeating-linear-gradient(135deg, rgb(34 197 94) 0 3px, rgb(148 163 184) 3px 6px)",
};

// cellsFor lays out one cell per IN endpoint the devices take now, in planner
// order. With a simulation, the endpoints it would add follow them as
// "added", in the places that are free now: the focused function's own
// first, then those of any other function, then the spares the planner
// would add so that no data endpoint lands on IN 6 or IN 8.
function cellsFor(
  current: EndpointUsage,
  simulated?: EndpointUsage | null,
  focus?: string | null,
): Cell[] {
  const want = new Map((simulated ?? current).items.map(i => [i.name, i.endpoints]));
  const cells: Cell[] = [];
  for (const item of current.items) {
    const kept = Math.min(item.endpoints, want.get(item.name) ?? 0);
    for (let i = 0; i < kept; i++) cells.push({ group: item.name, kind: "used" });
  }
  if (simulated) {
    const have = new Map(current.items.map(i => [i.name, i.endpoints]));
    const rank = (name: string) => (name === focus ? 0 : name === "spare" ? 2 : 1);
    const added = [...simulated.items].sort((a, b) => rank(a.name) - rank(b.name));
    for (const item of added) {
      const extra = item.endpoints - (have.get(item.name) ?? 0);
      for (let i = 0; i < extra; i++) cells.push({ group: item.name, kind: "added" });
    }
  }
  return cells;
}

// UsbEndpointBar: one cell per IN endpoint the devices take, green for a
// function's own, grey for the spares that keep data off IN 6 and IN 8.
// Hovering a cell shows what takes it, highlights that function's cells and
// reports the function (onHoverGroup) so the list can mark its rows. With a
// simulation (a feature row hovered that is off), the endpoints it would add
// follow the used ones at half strength, its own first, and a line says how
// many are its own and how many are spares; cells past the limit show red.
export function UsbEndpointBar({
  usage,
  simulated,
  highlight,
  onHoverGroup,
}: {
  usage: EndpointUsage | null;
  simulated?: EndpointUsage | null;
  // The function of the hovered feature row, on or off.
  highlight?: string | null;
  onHoverGroup?: (group: string | null) => void;
}) {
  const [hover, setHoverCell] = useState<number | null>(null);
  const cells = usage ? cellsFor(usage, simulated, highlight) : [];
  const setHover = (index: number | null) => {
    setHoverCell(index);
    onHoverGroup?.(index !== null ? (cells[index]?.group ?? null) : null);
  };
  if (!usage) return null;
  const total = usage.total;
  const slots = Math.max(total, cells.length);
  const shown = simulated ?? usage;
  const over = shown.used > total;
  const free = Math.max(0, total - shown.used);
  const hoverGroup = hover !== null ? cells[hover]?.group : null;
  const active = hoverGroup ?? highlight ?? null;
  const groupCount = (group: string) => cells.filter(c => c.group === group).length;

  let summary = m.usb_endpoints_used({
    used: usage.used,
    total,
    free: Math.max(0, total - usage.used),
  });
  if (simulated) {
    summary = over
      ? m.usb_endpoints_sim_over({ used: simulated.used, total })
      : m.usb_endpoints_sim_fits({ used: simulated.used, total, free });
  } else if (usage.used > total) {
    summary = m.usb_endpoints_over({ used: usage.used, total });
  }

  // What the simulated feature adds: its own endpoints and the reserved ones.
  const addedOwn = cells.filter(c => c.kind === "added" && c.group === highlight).length;
  const addedSpare = cells.filter(c => c.kind === "added" && c.group === "spare").length;
  const breakdown =
    simulated && highlight
      ? addedSpare > 0
        ? m.usb_endpoints_sim_breakdown_spare({
            name: endpointLabel(highlight),
            own: addedOwn,
            spare: addedSpare,
          })
        : m.usb_endpoints_sim_breakdown({ name: endpointLabel(highlight), own: addedOwn })
      : null;

  // One line whatever is hovered, so the list below never moves under the
  // pointer.
  const headline = breakdown ? `${breakdown} · ${summary}` : summary;

  return (
    <div className="space-y-2" aria-label={m.usb_endpoints_title()}>
      <div className="flex items-baseline justify-between gap-2">
        <span className="shrink-0 text-sm font-medium">{m.usb_endpoints_title()}</span>
        <span
          title={headline}
          className={cx("min-w-0 truncate text-xs whitespace-nowrap", {
            "font-semibold text-red-600 dark:text-red-400": over,
            "text-green-700 dark:text-green-400": simulated && !over,
            "text-slate-600 dark:text-slate-400": !simulated && !over,
          })}
        >
          {headline}
        </span>
      </div>
      <div className="relative">
        <div
          className="grid gap-1"
          style={{ gridTemplateColumns: `repeat(${slots}, minmax(0, 1fr))` }}
          role="meter"
          aria-valuemin={0}
          aria-valuemax={total}
          aria-valuenow={shown.used}
          onMouseLeave={() => setHover(null)}
        >
          {Array.from({ length: slots }, (_, i) => {
            const cell = cells[i];
            const beyond = i >= total;
            const spare = cell?.group === "spare";
            const lit = cell && active === cell.group;
            return (
              <div
                key={i}
                onMouseEnter={() => setHover(i)}
                style={spare && !beyond ? reservedStyle : undefined}
                className={cx("h-5 rounded-sm transition-all duration-150", {
                  "bg-slate-500/15": !cell,
                  "bg-green-500": cell && !spare && cell.kind === "used" && !beyond,
                  "bg-green-500/50": cell && !spare && cell.kind === "added" && !beyond,
                  "opacity-50": spare && cell.kind === "added" && !beyond,
                  "bg-red-500/50 ring-1 ring-red-500": cell && beyond,
                  "scale-y-125 ring-2 ring-green-700 dark:ring-green-300": lit,
                  "opacity-60": active && cell && cell.kind === "used" && !lit && !beyond,
                })}
              />
            );
          })}
        </div>
        {hover !== null && (
          <div
            role="tooltip"
            className="pointer-events-none absolute bottom-full z-10 mb-2 -translate-x-1/2 rounded bg-slate-900 px-2 py-1 text-xs whitespace-nowrap text-white shadow dark:bg-slate-100 dark:text-slate-900"
            style={{ left: `${((hover + 0.5) / slots) * 100}%` }}
          >
            {cells[hover]
              ? (cells[hover].group === "spare"
                  ? m.usb_endpoints_tooltip_spare({ count: groupCount("spare") })
                  : m.usb_endpoints_tooltip({
                      name: endpointLabel(cells[hover].group),
                      count: groupCount(cells[hover].group),
                    })) +
                (cells[hover].kind === "added" ? ` · ${m.usb_endpoints_tooltip_added()}` : "")
              : m.usb_endpoints_tooltip_free()}
            {hover >= total && ` · ${m.usb_endpoints_tooltip_over()}`}
          </div>
        )}
      </div>
      <p className="text-xs text-slate-500 dark:text-slate-400">{m.usb_endpoints_note()}</p>
    </div>
  );
}

const deviceKeys = Object.keys(none) as FeatureKey[];
const sameConfig = (a: UsbDeviceConfig, b: UsbDeviceConfig) =>
  deviceKeys.every(key => a[key] === b[key]);

export function UsbDeviceSetting() {
  const usbSerial = useCapability("usb_serial");
  const { send } = useJsonRpc();
  const [loading, setLoading] = useState(false);
  const { setUsbSerialConsoleEnabled } = useUiStore();

  // applied is what the KVM runs; draft is what the boxes show.
  const [applied, setApplied] = useState<UsbDeviceConfig>(defaultUsbDeviceConfig);
  const [draft, setDraft] = useState<UsbDeviceConfig>(defaultUsbDeviceConfig);
  const [usage, setUsage] = useState<EndpointUsage | null>(null);
  const [variants, setVariants] = useState<Partial<Record<FeatureKey, EndpointUsage>>>({});
  const [presetUsage, setPresetUsage] = useState<Record<string, number>>({});
  // The hovered row: a feature, or one of the read-only rows (USB wake-up,
  // OS Services), named by their endpoint group.
  const [hoverFeature, setHoverFeature] = useState<FeatureKey | FixedRow | null>(null);
  // The function whose cell in the endpoint bar is hovered.
  const [barGroup, setBarGroup] = useState<string | null>(null);

  const features = useMemo(
    () =>
      [
        {
          key: "keyboard",
          title: m.usb_device_enable_keyboard_title(),
          description: m.usb_device_enable_keyboard_description(),
        },
        {
          key: "absolute_mouse",
          title: m.usb_device_enable_absolute_mouse_title(),
          description: m.usb_device_enable_absolute_mouse_description(),
        },
        {
          key: "relative_mouse",
          title: m.usb_device_enable_relative_mouse_title(),
          description: m.usb_device_enable_relative_mouse_description(),
        },
        {
          key: "mass_storage",
          title: m.usb_device_enable_mass_storage_title(),
          description: m.usb_device_enable_mass_storage_description(),
        },
        {
          key: "audio",
          title: m.usb_device_enable_audio_title(),
          description: m.usb_device_enable_audio_description(),
        },
        ...(usbSerial
          ? [
              {
                key: "serial_console",
                title: m.usb_device_enable_serial_console_title(),
                description: m.usb_device_enable_serial_console_description(),
              },
            ]
          : []),
      ] as { key: FeatureKey; title: string; description: string }[],
    [usbSerial],
  );

  const sync = useCallback(() => {
    send("getUsbDevices", {}, (resp: JsonRpcResponse) => {
      if ("error" in resp) {
        notifications.error(
          m.usb_device_failed_load({ error: String(resp.error.data || m.unknown_error()) }),
        );
        return;
      }
      const config = { ...none, ...(resp.result as UsbDeviceConfig) };
      setApplied(config);
      setDraft(config);
      setUsbSerialConsoleEnabled(config.serial_console);
    });
  }, [send, setUsbSerialConsoleEnabled]);

  useEffect(() => sync(), [sync]);

  const usageOf = useCallback(
    (devices: UsbDeviceConfig, done: (usage: EndpointUsage) => void) =>
      send("getUsbEndpointUsage", { devices }, (resp: JsonRpcResponse) => {
        if (!("error" in resp)) done(resp.result as EndpointUsage);
      }),
    [send],
  );

  // The bar follows the draft; each feature's flip is asked for ahead, so a
  // hover shows its effect at once.
  useEffect(() => {
    usageOf(draft, setUsage);
    setVariants({});
    for (const key of deviceKeys) {
      usageOf({ ...draft, [key]: !draft[key] }, result =>
        setVariants(previous => ({ ...previous, [key]: result })),
      );
    }
  }, [draft, usageOf]);

  useEffect(() => {
    for (const preset of usbPresets) {
      if (!preset.config) continue;
      usageOf(preset.config, result =>
        setPresetUsage(previous => ({ ...previous, [preset.value]: result.used })),
      );
    }
  }, [usageOf]);

  const selectedPreset =
    usbPresets.find(p => p.config && sameConfig(p.config, draft))?.value ?? "custom";
  const total = usage?.total ?? 9;
  const presetOptions = usbPresets.map(preset => {
    const used = presetUsage[preset.value];
    if (!preset.config || used === undefined) return { label: preset.label, value: preset.value };
    return {
      label: `${preset.label} · ${m.usb_preset_endpoints({ used, total })}`,
      value: preset.value,
      disabled: used > total,
    };
  });
  const presetDescription = usbPresets.find(p => p.value === selectedPreset)?.description;
  const pending = !sameConfig(draft, applied);
  const fits = !usage || usage.used <= usage.total;

  // A hovered feature that is off is simulated; one that is on is highlighted.
  const simulated =
    hoverFeature && !isFixedRow(hoverFeature) && !draft[hoverFeature]
      ? (variants[hoverFeature] ?? null)
      : null;
  const highlight = !hoverFeature
    ? null
    : isFixedRow(hoverFeature)
      ? hoverFeature
      : featureGroup[hoverFeature];
  // OS Services is switched on its own page; while it is on, the list shows
  // what it takes.
  const osServicesEndpoints = usage?.items.find(i => i.name === "os_services")?.endpoints ?? 0;

  // A row that cannot be switched here; it highlights its endpoints.
  const fixedRow = (group: FixedRow, title: string, description: string, badge: string) => (
    <div
      onMouseEnter={() => setHoverFeature(group)}
      className={cx("rounded px-2 py-1.5 transition-colors", {
        "bg-green-50 dark:bg-green-900/20": hoverFeature === group,
        "bg-green-50 ring-1 ring-green-600/60 dark:bg-green-900/20 dark:ring-green-400/60":
          barGroup === group,
      })}
    >
      <SettingsItem size="SM" title={title} description={description}>
        <span className="rounded bg-green-100 px-2 py-0.5 text-xs font-medium whitespace-nowrap text-green-800 dark:bg-green-900/50 dark:text-green-300">
          {badge}
        </span>
      </SettingsItem>
    </div>
  );

  const apply = useCallback(
    (devices: UsbDeviceConfig) => {
      setLoading(true);
      send("setUsbDevices", { devices }, async (resp: JsonRpcResponse) => {
        if ("error" in resp) {
          notifications.error(
            m.usb_device_failed_set({ error: String(resp.error.data || m.unknown_error()) }),
          );
          setLoading(false);
          return;
        }
        // The USB devices need a moment to come back.
        await sleep(2000);
        setLoading(false);
        sync();
        notifications.success(m.usb_device_updated());
      });
    },
    [send, sync],
  );

  return (
    <Fieldset disabled={loading} className="space-y-4">
      <div className="h-px w-full bg-slate-800/10 dark:bg-slate-300/20" />
      <SettingsSectionHeader
        title={m.usb_device_title()}
        description={m.usb_device_description()}
      />

      <SettingsItem
        loading={loading}
        title={m.usb_device_classes_title()}
        description={m.usb_preset_pick_description()}
      >
        <SelectMenuBasic
          size="SM"
          label=""
          className="max-w-[292px]"
          value={selectedPreset}
          fullWidth
          onChange={e => {
            const preset = usbPresets.find(p => p.value === e.target.value);
            if (preset?.config) setDraft(preset.config);
          }}
          options={presetOptions}
        />
      </SettingsItem>
      {presetDescription && (
        <p className="-mt-2 text-xs text-slate-600 dark:text-slate-400">{presetDescription}</p>
      )}

      <UsbEndpointBar
        usage={usage}
        simulated={simulated}
        highlight={highlight}
        onHoverGroup={setBarGroup}
      />

      <div
        className="space-y-1 rounded-md border border-slate-800/10 p-2 dark:border-slate-300/20"
        onMouseLeave={() => setHoverFeature(null)}
      >
        {fixedRow(
          "wake",
          m.usb_device_wake_title(),
          m.usb_device_wake_description(),
          m.usb_device_wake_badge(),
        )}
        {features.map(feature => (
          <div
            key={feature.key}
            onMouseEnter={() => setHoverFeature(feature.key)}
            className={cx("rounded px-2 py-1.5 transition-colors", {
              "bg-green-50 dark:bg-green-900/20": hoverFeature === feature.key,
              "bg-green-50 ring-1 ring-green-600/60 dark:bg-green-900/20 dark:ring-green-400/60":
                barGroup !== null && featureGroup[feature.key] === barGroup,
            })}
          >
            <SettingsItem size="SM" title={feature.title} description={feature.description}>
              <Checkbox
                checked={draft[feature.key]}
                onChange={e =>
                  setDraft(previous => ({ ...previous, [feature.key]: e.target.checked }))
                }
                onFocus={() => setHoverFeature(feature.key)}
              />
            </SettingsItem>
          </div>
        ))}
        {osServicesEndpoints > 0 &&
          fixedRow(
            "os_services",
            m.usb_device_os_services_title(),
            m.usb_device_os_services_description(),
            m.usb_device_os_services_on(),
          )}
      </div>

      <div className="flex flex-wrap items-center gap-2">
        <Button
          size="SM"
          loading={loading}
          theme="primary"
          disabled={!pending || !fits}
          text={m.usb_device_update_classes()}
          onClick={() => apply(draft)}
        />
        <Button
          size="SM"
          theme="light"
          disabled={!pending}
          text={m.usb_device_discard()}
          onClick={() => setDraft(applied)}
        />
        <Button
          size="SM"
          theme="light"
          text={m.usb_device_restore_default()}
          onClick={() => setDraft(defaultUsbDeviceConfig)}
        />
        {pending && (
          <span className="text-xs text-amber-700 dark:text-amber-300">
            {fits ? m.usb_device_pending() : m.usb_device_pending_over()}
          </span>
        )}
      </div>
    </Fieldset>
  );
}
