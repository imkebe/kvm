import { Popover, PopoverButton, PopoverPanel } from "@headlessui/react";
import { LuCheck, LuChevronDown, LuCopy, LuNetwork } from "react-icons/lu";

import { cx } from "@/cva.config";
import StatusCard from "@components/StatusCards";
import { GridCard } from "@components/Card";
import { useCopyToClipboard } from "@components/useCopyToClipBoard";
import { netbirdStateLabel, useNetbirdState } from "@components/NetbirdCard";
import { useCapability } from "@hooks/stores";
import type { NetbirdState } from "@hooks/stores";
import { m } from "@localizations/messages.js";

// The computer behind the KVM is its own peer: USB Ethernet in NetBird mode
// with that peer joined.
function usbPeerOn(state: NetbirdState) {
  return state.usbShare && state.usbNetworkOn && !!state.target?.enabled;
}

// A copy button for addresses and names; a tick for a moment after copying.
function CopyButton({ value }: { value: string }) {
  const { copy, isCopied } = useCopyToClipboard();
  const Icon = isCopied ? LuCheck : LuCopy;
  return (
    <button
      type="button"
      title={isCopied ? m.netbird_card_copied() : m.netbird_card_copy()}
      aria-label={m.netbird_card_copy()}
      onClick={() => void copy(value)}
      className="shrink-0 rounded p-0.5 text-slate-500 hover:bg-slate-100 hover:text-slate-800 dark:text-slate-400 dark:hover:bg-slate-700 dark:hover:text-slate-100"
    >
      <Icon className={isCopied ? "h-3.5 w-3.5 text-green-600" : "h-3.5 w-3.5"} />
    </button>
  );
}

function Row({ label, value, copyable }: { label: string; value?: string; copyable?: boolean }) {
  return (
    <>
      <dt className="text-slate-500 dark:text-slate-400">{label}</dt>
      <dd className="flex min-w-0 items-center gap-x-1">
        <span className="truncate font-mono select-text" title={value}>
          {value || "—"}
        </span>
        {copyable && value && <CopyButton value={value} />}
      </dd>
    </>
  );
}

// The addresses behind the card's button.
function AddressesButton({ state }: { state: NetbirdState }) {
  const target = state.target;
  const usb = usbPeerOn(state);
  return (
    <Popover>
      <PopoverButton
        title={m.netbird_card_addresses()}
        aria-label={m.netbird_card_addresses()}
        className="flex h-7 w-7 items-center justify-center rounded-md border border-slate-800/20 bg-white text-slate-700 shadow-xs transition-colors hover:bg-slate-50 focus:outline-none focus-visible:ring-2 focus-visible:ring-blue-400 data-open:bg-slate-100 dark:border-slate-600 dark:bg-slate-700 dark:text-slate-200 dark:hover:bg-slate-600"
      >
        <LuChevronDown className="h-4 w-4" />
      </PopoverButton>
      <PopoverPanel
        anchor="bottom end"
        transition
        className={cx(
          "z-30 flex w-[min(340px,calc(100vw-24px))] origin-top flex-col overflow-visible! [--anchor-gap:8px]",
          "transition duration-200 ease-out data-closed:translate-y-2 data-closed:opacity-0",
        )}
      >
        <GridCard>
          <div className="space-y-3 p-3 text-xs text-slate-900 dark:text-slate-100">
            <div className="space-y-1">
              <div className="font-semibold">{m.netbird_card_kvm()}</div>
              <dl className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-0.5">
                <Row label={m.netbird_target_state()} value={netbirdStateLabel(state)} />
                <Row label={m.netbird_ip()} value={state.status?.ip} copyable />
                <Row label={m.netbird_name()} value={state.status?.fqdn} copyable />
              </dl>
            </div>
            {usb && (
              <div className="space-y-1 border-t border-slate-800/10 pt-2 dark:border-slate-300/20">
                <div className="font-semibold">{m.netbird_card_usb()}</div>
                <dl className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-0.5">
                  <Row
                    label={m.netbird_target_state()}
                    value={target.status?.daemonStatus ?? m.netbird_state_off()}
                  />
                  <Row
                    label={m.netbird_ip()}
                    value={target.address || target.status?.ip}
                    copyable
                  />
                  <Row label={m.netbird_name()} value={target.status?.fqdn} copyable />
                  {target.dnsServer && (
                    <Row label={m.netbird_card_dns()} value={target.dnsServer} copyable />
                  )}
                </dl>
              </div>
            )}
          </div>
        </GridCard>
      </PopoverPanel>
    </Popover>
  );
}

// Ribbon card: "NetBird KVM", or "NetBird KVM + USB" while the computer
// behind the KVM is its own peer; "Active" once connected. The addresses are
// behind the button. Shown only while NetBird is turned on.
export default function NetbirdStatusCard() {
  const hasNetbird = useCapability("netbird");
  const { state } = useNetbirdState();
  if (!hasNetbird || !state?.installed || !state.enabled) return null;

  const usb = usbPeerOn(state);
  const kvmConnected = state.status?.daemonStatus === "Connected";
  const usbConnected = !usb || state.target?.status?.daemonStatus === "Connected";
  const status = kvmConnected ? m.netbird_card_active() : netbirdStateLabel(state);

  return (
    <div className="hidden w-[220px] xl:block">
      <StatusCard
        title={usb ? m.netbird_card_title_usb() : m.netbird_card_title()}
        status={status}
        icon={({ className }) => (
          <LuNetwork className={cx(className, "h-5 w-5 text-slate-700 dark:text-slate-300")} />
        )}
        statusIndicatorClassName={
          kvmConnected && usbConnected
            ? "border-green-600 bg-green-500"
            : "border-amber-500 bg-amber-400 dark:border-amber-500 dark:bg-amber-500"
        }
        action={<AddressesButton state={state} />}
      />
    </div>
  );
}
