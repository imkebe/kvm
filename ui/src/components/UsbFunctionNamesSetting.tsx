import { useCallback, useEffect, useState } from "react";

import { JsonRpcResponse, useJsonRpc } from "@hooks/useJsonRpc";
import { Button } from "@components/Button";
import { ConfirmDialog } from "@components/ConfirmDialog";
import Fieldset from "@components/Fieldset";
import InputField from "@components/InputField";
import { SelectMenuBasic } from "@components/SelectMenuBasic";
import { SettingsItem } from "@components/SettingsItem";
import { SettingsSectionHeader } from "@components/SettingsSectionHeader";
import notifications from "@/notifications";
import { sleep } from "@/utils";
import { m } from "@localizations/messages.js";

interface FunctionNames {
  camera_name: string;
  audio_name: string;
  storage_vendor: string;
  storage_model: string;
}

const empty: FunctionNames = {
  camera_name: "",
  audio_name: "",
  storage_vendor: "",
  storage_model: "",
};

// Names the target computer shows for single functions. The keyboard and
// mice have no name of their own: they show the USB identity below. The
// first entry of each list is the KVM's default ("" is stored for it).
const cameraNames = ["UVC Camera", "JetKVM Camera", "USB Camera", "HD Webcam", "Integrated Camera"];
const audioNames = [
  "Source/Sink",
  "JetKVM Audio",
  "USB Audio",
  "USB Audio Device",
  "USB Microphone",
];
const storageNames: [string, string][] = [
  ["JetKVM", "Virtual Media"],
  ["Generic", "Flash Disk"],
  ["Generic", "Mass Storage"],
  ["USB", "DISK 2.0"],
];
const CUSTOM = "__custom";

function NameSelect({
  title,
  description,
  names,
  value,
  max,
  onChange,
}: {
  title: string;
  description: string;
  names: string[];
  value: string;
  max: number;
  onChange: (value: string) => void;
}) {
  const shown = value || names[0];
  const [custom, setCustom] = useState(!names.includes(shown));
  useEffect(() => {
    if (!names.includes(value || names[0])) setCustom(true);
  }, [value, names]);
  return (
    <SettingsItem title={title} description={description}>
      <div className="flex w-64 flex-col gap-1">
        <SelectMenuBasic
          size="SM"
          label=""
          fullWidth
          value={custom ? CUSTOM : shown}
          onChange={e => {
            if (e.target.value === CUSTOM) {
              setCustom(true);
              return;
            }
            setCustom(false);
            onChange(e.target.value === names[0] ? "" : e.target.value);
          }}
          options={[
            ...names.map((name, i) => ({
              value: name,
              label: i === 0 ? m.usb_names_default({ name }) : name,
            })),
            { value: CUSTOM, label: m.usb_names_custom() },
          ]}
        />
        {custom && (
          <InputField
            size="SM"
            value={value}
            maxLength={max}
            placeholder={names[0]}
            onChange={e => onChange(e.target.value)}
          />
        )}
      </div>
    </SettingsItem>
  );
}

export function UsbFunctionNamesSetting() {
  const { send } = useJsonRpc();
  const [applied, setApplied] = useState<FunctionNames>(empty);
  const [draft, setDraft] = useState<FunctionNames>(empty);
  const [confirm, setConfirm] = useState(false);
  const [saving, setSaving] = useState(false);
  const [storageCustom, setStorageCustom] = useState(false);

  const load = useCallback(() => {
    send("getUsbFunctionNames", {}, (resp: JsonRpcResponse) => {
      if ("error" in resp) return;
      const names = { ...empty, ...(resp.result as Partial<FunctionNames>) };
      setApplied(names);
      setDraft(names);
      const vendor = names.storage_vendor || storageNames[0][0];
      const model = names.storage_model || storageNames[0][1];
      setStorageCustom(!storageNames.some(([v, p]) => v === vendor && p === model));
    });
  }, [send]);
  useEffect(() => load(), [load]);

  const pending = (Object.keys(empty) as (keyof FunctionNames)[]).some(
    k => draft[k] !== applied[k],
  );
  const storageValue = storageCustom
    ? CUSTOM
    : `${draft.storage_vendor || storageNames[0][0]}|${draft.storage_model || storageNames[0][1]}`;

  const save = () => {
    setSaving(true);
    send("setUsbFunctionNames", { names: draft }, async (resp: JsonRpcResponse) => {
      if ("error" in resp) {
        setSaving(false);
        setConfirm(false);
        notifications.error(
          m.usb_names_failed({ error: String(resp.error.data || m.unknown_error()) }),
        );
        return;
      }
      await sleep(2000);
      setSaving(false);
      setConfirm(false);
      load();
      notifications.success(m.usb_names_saved());
    });
  };

  return (
    <Fieldset disabled={saving} className="space-y-4">
      <div className="h-px w-full bg-slate-800/10 dark:bg-slate-300/20" />
      <SettingsSectionHeader title={m.usb_names_title()} description={m.usb_names_description()} />
      <NameSelect
        title={m.usb_names_camera()}
        description={m.usb_names_camera_description()}
        names={cameraNames}
        value={draft.camera_name}
        max={31}
        onChange={camera_name => setDraft(previous => ({ ...previous, camera_name }))}
      />
      <NameSelect
        title={m.usb_names_audio()}
        description={m.usb_names_audio_description()}
        names={audioNames}
        value={draft.audio_name}
        max={31}
        onChange={audio_name => setDraft(previous => ({ ...previous, audio_name }))}
      />
      <SettingsItem title={m.usb_names_storage()} description={m.usb_names_storage_description()}>
        <div className="flex w-64 flex-col gap-1">
          <SelectMenuBasic
            size="SM"
            label=""
            fullWidth
            value={storageValue}
            onChange={e => {
              if (e.target.value === CUSTOM) {
                setStorageCustom(true);
                return;
              }
              setStorageCustom(false);
              const [vendor, model] = e.target.value.split("|");
              const isDefault = vendor === storageNames[0][0] && model === storageNames[0][1];
              setDraft(previous => ({
                ...previous,
                storage_vendor: isDefault ? "" : vendor,
                storage_model: isDefault ? "" : model,
              }));
            }}
            options={[
              ...storageNames.map(([vendor, model], i) => ({
                value: `${vendor}|${model}`,
                label:
                  i === 0
                    ? m.usb_names_default({ name: `${vendor} ${model}` })
                    : `${vendor} ${model}`,
              })),
              { value: CUSTOM, label: m.usb_names_custom() },
            ]}
          />
          {storageCustom && (
            <div className="flex gap-1">
              <InputField
                size="SM"
                className="w-24"
                value={draft.storage_vendor}
                maxLength={8}
                placeholder={m.usb_names_vendor()}
                onChange={e =>
                  setDraft(previous => ({ ...previous, storage_vendor: e.target.value }))
                }
              />
              <InputField
                size="SM"
                value={draft.storage_model}
                maxLength={16}
                placeholder={m.usb_names_model()}
                onChange={e =>
                  setDraft(previous => ({ ...previous, storage_model: e.target.value }))
                }
              />
            </div>
          )}
        </div>
      </SettingsItem>
      <p className="text-xs text-slate-500 dark:text-slate-400">{m.usb_names_hid_note()}</p>
      <div className="flex gap-2">
        <Button
          size="SM"
          theme="primary"
          disabled={!pending}
          text={m.usb_names_apply()}
          onClick={() => setConfirm(true)}
        />
        <Button
          size="SM"
          theme="light"
          disabled={!pending}
          text={m.usb_device_discard()}
          onClick={() => setDraft(applied)}
        />
      </div>
      <ConfirmDialog
        open={confirm}
        onClose={() => setConfirm(false)}
        title={m.usb_names_confirm_title()}
        description={m.usb_names_confirm_description()}
        variant="warning"
        confirmText={m.usb_names_apply()}
        onConfirm={save}
        isConfirming={saving}
      />
    </Fieldset>
  );
}
