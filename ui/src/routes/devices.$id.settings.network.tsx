import { useCallback, useEffect, useRef, useState } from "react";
import { FieldValues, FormProvider, useForm } from "react-hook-form";
import { LuCopy, LuEthernetPort, LuRadar } from "react-icons/lu";
import dayjs from "dayjs";
import relativeTime from "dayjs/plugin/relativeTime";
import validator from "validator";

import WifiNetworksCard from "@components/WifiNetworksCard";
import PublicIPCard from "@components/PublicIPCard";
import {
  NetworkSettings,
  NetworkState,
  useCapability,
  useNetworkStateStore,
  useRTCStore,
} from "@hooks/stores";
import { JsonRpcResponse, useJsonRpc } from "@hooks/useJsonRpc";
import { useNetworkDiscovery } from "@hooks/useNetworkDiscovery";
import AutoHeight from "@components/AutoHeight";
import { Button, LinkButton } from "@components/Button";
import { Checkbox } from "@components/Checkbox";
import { ConfirmDialog } from "@components/ConfirmDialog";
import CustomTimeSyncCard from "@components/CustomTimeSyncCard";
import DhcpLeaseCard from "@components/DhcpLeaseCard";
import EmptyCard from "@components/EmptyCard";
import { GridCard } from "@components/Card";
import InputField, { InputFieldWithLabel } from "@components/InputField";
import Ipv6NetworkCard from "@components/Ipv6NetworkCard";
import NetworkDiscoveryDialog from "@components/NetworkDiscoveryDialog";
import { SelectMenuBasic } from "@/components/SelectMenuBasic";
import { SettingsItem } from "@components/SettingsItem";
import { SettingsPageHeader } from "@/components/SettingsPageheader";
import StaticIpv4Card from "@components/StaticIpv4Card";
import StaticIpv6Card from "@components/StaticIpv6Card";
import { useCopyToClipboard } from "@components/useCopyToClipBoard";
import { netMaskFromCidr4 } from "@/utils/ip";
import { getNetworkSettings, getNetworkState } from "@/utils/jsonrpc";
import notifications from "@/notifications";
import { m } from "@localizations/messages";

dayjs.extend(relativeTime);

const isLLDPAvailable = false; // LLDP is not supported yet

const resolveOnRtcReady = () => {
  return new Promise(resolve => {
    // Check if RTC is already connected
    const currentState = useRTCStore.getState();
    if (currentState.rpcDataChannel?.readyState === "open") {
      // Already connected, fetch data immediately
      return resolve(void 0);
    }

    // Not connected yet, subscribe to state changes
    const unsubscribe = useRTCStore.subscribe(state => {
      if (state.rpcDataChannel?.readyState === "open") {
        unsubscribe(); // Clean up subscription
        return resolve(void 0);
      }
    });
  });
};

export function LifeTimeLabel({ lifetime }: Readonly<{ lifetime: string }>) {
  const [remaining, setRemaining] = useState<string | null>(null);

  // rrecalculate remaining time every 30 seconds
  useEffect(() => {
    setRemaining(dayjs(lifetime).fromNow());

    const interval = setInterval(() => {
      setRemaining(dayjs(lifetime).fromNow());
    }, 1000 * 30);
    return () => clearInterval(interval);
  }, [lifetime]);

  if (lifetime == "") {
    return <strong>{m.not_applicable()}</strong>;
  }

  return (
    <>
      <span className="text-sm font-medium">{remaining && <> {remaining}</>}</span>
      <span className="text-xs text-slate-700 dark:text-slate-300">
        &nbsp;({dayjs(lifetime).format("YYYY-MM-DD HH:mm")})
      </span>
    </>
  );
}

const NonCustomDomainOptions = ["dhcp", "local"];
const DhcpClientOptions = ["jetdhcpc", "udhcpc"];

export default function SettingsNetworkRoute() {
  const { send } = useJsonRpc();

  const networkState = useNetworkStateStore(state => state);
  // A device hides the settings it cannot change by not reporting them.
  const hasHttpProxy = useCapability("http_proxy");
  const hasDomain = useCapability("domain");
  const hasMdns = useCapability("mdns");
  const hasIpv6 = useCapability("ipv6");
  const hasWifi = useCapability("wifi");
  const hasTailscale = useCapability("tailscale");
  const hasNetbird = useCapability("netbird");
  const setNetworkState = useNetworkStateStore(state => state.setNetworkState);

  // Some input needs direct state management. Mostly options that open more details
  const [customDomain, setCustomDomain] = useState<string>("");

  // Confirm dialog
  const [isCloudAdopted, setIsCloudAdopted] = useState(false);
  const [showRenewLeaseConfirm, setShowRenewLeaseConfirm] = useState(false);
  const [discoveryOpen, setDiscoveryOpen] = useState(false);
  const { checkAddress } = useNetworkDiscovery();
  // Addresses another device answers for that the user was warned about:
  // saving again with the same address goes ahead.
  const acknowledgedInUse = useRef(new Set<string>());

  // We use this to determine whether the settings have changed
  const initialSettingsRef = useRef<NetworkSettings | null>(null);

  const [showCriticalSettingsConfirm, setShowCriticalSettingsConfirm] = useState(false);
  const [stagedSettings, setStagedSettings] = useState<NetworkSettings | null>(null);
  const [criticalChanges, setCriticalChanges] = useState<
    { label: string; from: string; to: string }[]
  >([]);

  const fetchNetworkData = useCallback(async () => {
    try {
      console.log("Fetching network data...");

      const [settings, state] = (await Promise.all([getNetworkSettings(), getNetworkState()])) as [
        NetworkSettings,
        NetworkState,
      ];

      setNetworkState(state);

      const settingsWithDefaults = {
        ...settings,

        domain: settings.domain || "local", // TODO: null means local domain TRUE?????
        mdns_mode: settings.mdns_mode || "disabled",
        time_sync_mode: settings.time_sync_mode || "ntp_only",
        ipv4_static: {
          address: settings.ipv4_static?.address || state.dhcp_lease?.ip || "",
          netmask: settings.ipv4_static?.netmask || state.dhcp_lease?.netmask || "",
          gateway: settings.ipv4_static?.gateway || state.dhcp_lease?.routers?.[0] || "",
          dns: settings.ipv4_static?.dns || state.dhcp_lease?.dns_servers || [],
        },
        ipv4_fallback_enabled: !!settings.ipv4_fallback_enabled,
        ipv4_fallback_timeout: settings.ipv4_fallback_timeout || 30,
        ipv4_fallback: {
          address: settings.ipv4_fallback?.address || "",
          netmask: settings.ipv4_fallback?.netmask || "",
          gateway: settings.ipv4_fallback?.gateway || "",
          dns: settings.ipv4_fallback?.dns || [],
        },
        ipv6_static: {
          prefix: settings.ipv6_static?.prefix || state.ipv6_addresses?.[0]?.prefix || "",
          gateway: settings.ipv6_static?.gateway || "",
          dns: settings.ipv6_static?.dns || [],
        },
      };

      if (!NonCustomDomainOptions.includes(settingsWithDefaults.domain)) {
        setCustomDomain(settingsWithDefaults.domain);
        settingsWithDefaults.domain = "custom";
      }

      initialSettingsRef.current = settingsWithDefaults;
      return { settings: settingsWithDefaults, state };
    } catch (err) {
      notifications.error(
        m.network_settings_load_error({
          error: err instanceof Error ? err.message : m.unknown_error(),
        }),
      );
      throw err;
    }
  }, [setNetworkState, setCustomDomain]);

  useEffect(() => {
    send("getCloudState", {}, (resp: JsonRpcResponse) => {
      if ("result" in resp) {
        const state = resp.result as { connected: boolean };
        setIsCloudAdopted(state.connected);
      }
    });
  }, [send]);

  const formMethods = useForm<NetworkSettings>({
    mode: "onBlur",

    defaultValues: async () => {
      // Ensure data channel is ready, before fetching network data from the device
      await resolveOnRtcReady();
      const { settings } = await fetchNetworkData();
      return settings;
    },
  });

  const prepareSettings = useCallback(
    (data: FieldValues) => {
      return {
        ...data,

        // If custom domain option is selected, use the custom domain as value
        domain: data.domain === "custom" ? customDomain : data.domain,
      } as NetworkSettings;
    },
    [customDomain],
  );

  const { register, handleSubmit, watch, formState, reset, getValues, setValue, setError } =
    formMethods;
  const reloadSettings = useCallback(
    () =>
      fetchNetworkData().then(
        ({ settings }) => {
          // IPv4 belongs to the network just joined; other unsaved edits, such as
          // the hostname, are device-wide and stay unsaved.
          const edited = getValues();
          const kept = (Object.keys(formState.dirtyFields) as (keyof NetworkSettings)[]).filter(
            key => !key.startsWith("ipv4"),
          );
          reset(settings);
          for (const key of kept) setValue(key, edited[key], { shouldDirty: true });
        },
        () => undefined,
      ),
    [fetchNetworkData, formState, getValues, reset, setValue],
  );

  const onSubmit = useCallback(
    async (settings: NetworkSettings) => {
      if (settings.ipv4_static?.address?.includes("/")) {
        const parts = settings.ipv4_static.address.split("/");
        const cidrNotation = Number.parseInt(parts[1]);
        if (Number.isNaN(cidrNotation) || cidrNotation < 0 || cidrNotation > 32) {
          return notifications.error(m.network_ipv4_invalid_cidr());
        }
        settings.ipv4_static.netmask = netMaskFromCidr4(cidrNotation);
        settings.ipv4_static.address = parts[0];
      }

      if (settings.ipv4_static) {
        settings.ipv4_static.dns = settings.ipv4_static.dns.filter(Boolean);
      }

      // The fallback, like the static settings, takes an address in CIDR
      // notation. Without an address and switched off it is not sent: the
      // KVM requires the address of a fallback it receives.
      const fallback = settings.ipv4_fallback;
      if (fallback?.address?.includes("/")) {
        const [address, bits] = fallback.address.split("/");
        const cidrNotation = Number.parseInt(bits);
        if (Number.isNaN(cidrNotation) || cidrNotation < 0 || cidrNotation > 32) {
          return notifications.error(m.network_ipv4_invalid_cidr());
        }
        fallback.netmask = netMaskFromCidr4(cidrNotation);
        fallback.address = address;
      }
      if (fallback) fallback.dns = fallback.dns.filter(Boolean);
      if (fallback && !fallback.address && !settings.ipv4_fallback_enabled) {
        delete settings.ipv4_fallback;
      }
      settings.ipv4_fallback_timeout = Number(settings.ipv4_fallback_timeout) || 30;

      send("setNetworkSettings", { settings }, async resp => {
        if ("error" in resp) {
          notifications.error(
            m.network_save_settings_failed({ error: resp.error.message || m.unknown_error() }),
          );
        } else {
          // If the settings are saved successfully, fetch the latest network data and reset the form
          // We do this so we get all the form state values, for stuff like is the form dirty, etc...

          try {
            const networkData = await fetchNetworkData();
            if (!networkData) return;

            reset(networkData.settings);
            notifications.success(m.network_save_settings_success());
          } catch (error) {
            console.error("Failed to fetch network data:", error);
          }
          notifications.success(m.network_dhcp_lease_renew_success());
        }
      });
    },
    [fetchNetworkData, reset, send],
  );

  const onSubmitGate = useCallback(
    async (data: FieldValues) => {
      const settings = prepareSettings(data);
      const dirty = formState.dirtyFields;

      // A new static or fallback address is checked first: if another
      // device answers ARP for it, the field shows that, and saving again
      // with the same address goes ahead.
      const addressOf = (value?: string) => (value ?? "").split("/")[0];
      const checks: { field: "ipv4_static.address" | "ipv4_fallback.address"; address: string }[] =
        [];
      if (settings.ipv4_mode === "static" && (dirty.ipv4_mode || dirty.ipv4_static?.address)) {
        checks.push({
          field: "ipv4_static.address",
          address: addressOf(settings.ipv4_static?.address),
        });
      }
      if (
        settings.ipv4_mode === "dhcp" &&
        settings.ipv4_fallback_enabled &&
        (dirty.ipv4_fallback_enabled || dirty.ipv4_fallback?.address)
      ) {
        checks.push({
          field: "ipv4_fallback.address",
          address: addressOf(settings.ipv4_fallback?.address),
        });
      }
      for (const check of checks) {
        if (!validator.isIP(check.address, 4) || acknowledgedInUse.current.has(check.address)) {
          continue;
        }
        try {
          const result = await checkAddress(check.address);
          if (result.in_use) {
            acknowledgedInUse.current.add(check.address);
            setError(check.field, {
              type: "in_use",
              message: m.network_address_in_use_save({ mac: result.mac ?? "" }),
            });
            return;
          }
        } catch {
          // The check could not run, for example during another scan:
          // the address is not known to be taken.
        }
      }

      // Build list of critical changes for display
      const changes: { label: string; from: string; to: string }[] = [];

      if (dirty.dhcp_client) {
        changes.push({
          label: m.network_dhcp_client_title(),
          from: initialSettingsRef.current?.dhcp_client as string,
          to: data.dhcp_client as string,
        });
      }

      if (dirty.ipv4_mode) {
        changes.push({
          label: m.network_ipv4_mode_title(),
          from: initialSettingsRef.current?.ipv4_mode as string,
          to: data.ipv4_mode as string,
        });
      }

      if (dirty.ipv4_static?.address) {
        changes.push({
          label: m.network_ipv4_address(),
          from: initialSettingsRef.current?.ipv4_static?.address as string,
          to: data.ipv4_static?.address as string,
        });
      }

      if (dirty.ipv4_static?.netmask) {
        changes.push({
          label: m.network_ipv4_netmask(),
          from: initialSettingsRef.current?.ipv4_static?.netmask as string,
          to: data.ipv4_static?.netmask as string,
        });
      }

      if (dirty.ipv4_static?.gateway) {
        changes.push({
          label: m.network_ipv4_gateway(),
          from: initialSettingsRef.current?.ipv4_static?.gateway as string,
          to: data.ipv4_static?.gateway as string,
        });
      }

      if (
        dirty.ipv4_static?.dns &&
        dirty.ipv4_static.dns.length > 0 &&
        dirty.ipv4_static.dns.every(dirty => dirty)
      ) {
        changes.push({
          label: m.network_ipv4_dns(),
          from: initialSettingsRef.current?.ipv4_static?.dns.join(", ").toString() ?? "",
          to: data.ipv4_static?.dns.join(", ").toString() ?? "",
        });
      }

      if (dirty.ipv6_mode) {
        changes.push({
          label: m.network_ipv6_mode_title(),
          from: initialSettingsRef.current?.ipv6_mode as string,
          to: data.ipv6_mode as string,
        });
      }

      if (dirty.ipv6_static?.prefix) {
        changes.push({
          label: m.network_ipv6_prefix(),
          from: initialSettingsRef.current?.ipv6_static?.prefix as string,
          to: data.ipv6_static?.prefix as string,
        });
      }

      if (dirty.ipv6_static?.gateway) {
        changes.push({
          label: m.network_ipv6_gateway(),
          from: initialSettingsRef.current?.ipv6_static?.gateway as string,
          to: data.ipv6_static?.gateway as string,
        });
      }

      if (
        dirty.ipv6_static?.dns &&
        dirty.ipv6_static.dns.length > 0 &&
        dirty.ipv6_static.dns.every(dirty => dirty)
      ) {
        changes.push({
          label: m.network_ipv6_dns(),
          from: initialSettingsRef.current?.ipv6_static?.dns.join(", ").toString() ?? "",
          to: data.ipv6_static?.dns.join(", ").toString() ?? "",
        });
      }

      if (dirty.hostname) {
        changes.push({
          label: m.network_hostname_title(),
          from: initialSettingsRef.current?.hostname?.toString() ?? "",
          to: data.hostname?.toString() ?? "",
        });
      }

      // If no critical fields are changed, save immediately
      if (changes.length === 0) return onSubmit(settings);

      // Show confirmation dialog for critical changes
      setStagedSettings(settings);
      setCriticalChanges(changes);
      setShowCriticalSettingsConfirm(true);
    },
    [prepareSettings, formState.dirtyFields, onSubmit, checkAddress, setError],
  );

  const dhcpClient = watch("dhcp_client");
  const ipv4mode = watch("ipv4_mode");
  const ipv6mode = watch("ipv6_mode");
  const domain = watch("domain");
  const timeSyncMode = watch("time_sync_mode");
  const fallbackEnabled = watch("ipv4_fallback_enabled");

  const onDhcpLeaseRenew = () => {
    send("renewDHCPLease", {}, resp => {
      if ("error" in resp) {
        notifications.error(
          m.network_dhcp_lease_renew_failed({ error: resp.error.message || m.unknown_error() }),
        );
      } else {
        notifications.success(m.network_dhcp_lease_renew_success());
      }
    });
  };

  const { copy } = useCopyToClipboard();

  return (
    <>
      <FormProvider {...formMethods}>
        {/* This is a bug in react. The fix hasn'e been released yet. https://github.com/facebook/react/pull/35062 */}
        {}
        <form onSubmit={handleSubmit(onSubmitGate)} className="space-y-4">
          <SettingsPageHeader
            title={m.network_title()}
            description={m.network_description()}
            action={
              <div>
                <Button
                  size="SM"
                  theme="primary"
                  disabled={!(formState.isDirty || formState.isSubmitting)}
                  loading={formState.isSubmitting}
                  type="submit"
                  text={formState.isSubmitting ? m.saving() : m.network_save_settings()}
                />
              </div>
            }
          />
          <div className="space-y-4">
            {hasWifi && <WifiNetworksCard onSwitched={reloadSettings} />}
            <div className="flex items-center justify-between">
              <SettingsItem
                title={m.network_mac_address_title()}
                description={m.network_mac_address_description()}
              />
              <div className="flex items-center">
                <GridCard cardClassName="rounded-r-none">
                  <div className="flex h-[34px] items-center px-3 font-mono text-xs text-black select-all dark:text-white">
                    {networkState?.mac_address}{" "}
                  </div>
                </GridCard>
                <Button
                  className="rounded-l-none border-l-slate-800/30 dark:border-slate-300/20"
                  size="SM"
                  type="button"
                  theme="light"
                  LeadingIcon={LuCopy}
                  onClick={async () => {
                    const mac = networkState?.mac_address || "";
                    if (await copy(mac)) {
                      notifications.success(m.network_mac_address_copy_success({ mac: mac }));
                    } else {
                      notifications.error(m.network_mac_address_copy_error());
                    }
                  }}
                />
              </div>
            </div>

            <div className="space-y-4">
              <SettingsItem
                title={m.network_hostname_title()}
                description={m.network_hostname_description()}
              >
                <InputField
                  size="SM"
                  placeholder={networkState?.hostname || "jetkvm"}
                  {...register("hostname")}
                  error={formState.errors.hostname?.message}
                />
              </SettingsItem>

              {hasHttpProxy && (
                <SettingsItem
                  title={m.network_http_proxy_title()}
                  description={m.network_http_proxy_description()}
                >
                  <InputField
                    size="SM"
                    placeholder="http://proxy.example.com:8080"
                    {...register("http_proxy", {
                      validate: (value: string | null) => {
                        if (value === "" || value === null) return true;
                        if (!validator.isURL(value || "", { protocols: ["http", "https"] })) {
                          return m.network_http_proxy_invalid();
                        }
                        return true;
                      },
                    })}
                    error={formState.errors.http_proxy?.message}
                  />
                </SettingsItem>
              )}

              {hasDomain && (
                <div className="space-y-1">
                  <SettingsItem
                    title={m.network_domain_title()}
                    description={m.network_domain_description()}
                  >
                    <div className="space-y-2">
                      <SelectMenuBasic
                        size="SM"
                        options={[
                          { value: "dhcp", label: m.network_domain_dhcp_provided() },
                          { value: "local", label: m.network_domain_local() },
                          { value: "custom", label: m.network_domain_custom() },
                        ]}
                        {...register("domain")}
                        error={formState.errors.domain?.message}
                      />
                    </div>
                  </SettingsItem>

                  {domain === "custom" && (
                    <div className="mt-2 w-1/3 border-l border-slate-800/10 pl-4 dark:border-slate-300/20">
                      <InputFieldWithLabel
                        size="SM"
                        type="text"
                        label={m.network_custom_domain()}
                        placeholder="home.example.com"
                        value={customDomain}
                        onChange={e => {
                          setCustomDomain(e.target.value);
                        }}
                      />
                    </div>
                  )}
                </div>
              )}

              {hasMdns && (
                <SettingsItem
                  title={m.network_mdns_title()}
                  description={m.network_mdns_description()}
                >
                  <SelectMenuBasic
                    size="SM"
                    options={[
                      { value: "disabled", label: m.network_mdns_disabled() },
                      { value: "auto", label: m.network_mdns_auto() },
                      { value: "ipv4_only", label: m.network_mdns_ipv4_only() },
                      { value: "ipv6_only", label: m.network_mdns_ipv6_only() },
                    ]}
                    {...register("mdns_mode")}
                  />
                </SettingsItem>
              )}

              <SettingsItem
                title={m.network_time_sync_title()}
                description={m.network_time_sync_description()}
              >
                <SelectMenuBasic
                  size="SM"
                  options={[
                    // { value: "auto", label: "Auto" },
                    { value: "ntp_only", label: m.network_time_sync_ntp_only() },
                    { value: "ntp_and_http", label: m.network_time_sync_ntp_and_http() },
                    { value: "http_only", label: m.network_time_sync_http_only() },
                    { value: "custom", label: m.network_time_sync_custom() },
                  ]}
                  {...register("time_sync_mode")}
                />
              </SettingsItem>

              {timeSyncMode === "custom" && <CustomTimeSyncCard />}

              <SettingsItem
                title={m.network_dhcp_client_title()}
                description={m.network_dhcp_client_description()}
              >
                {/* A device with a single, fixed DHCP client reports its own name. */}
                {dhcpClient && !DhcpClientOptions.includes(dhcpClient) ? (
                  <span className="font-mono text-sm text-black dark:text-white">{dhcpClient}</span>
                ) : (
                  <SelectMenuBasic
                    size="SM"
                    options={[
                      { value: "jetdhcpc", label: m.network_dhcp_client_jetkvm() },
                      { value: "udhcpc", label: "udhcpc" }, // do not localize
                    ]}
                    {...register("dhcp_client")}
                  />
                )}
              </SettingsItem>

              <SettingsItem
                title={m.network_ipv4_mode_title()}
                description={m.network_ipv4_mode_description()}
              >
                <SelectMenuBasic
                  size="SM"
                  options={[
                    { value: "dhcp", label: m.network_ipv4_mode_dhcp() },
                    { value: "static", label: m.network_ipv4_mode_static() },
                  ]}
                  {...register("ipv4_mode")}
                />
              </SettingsItem>

              <SettingsItem
                title={m.network_discovery_title()}
                description={m.network_discovery_item_description()}
              >
                <Button
                  size="SM"
                  theme="light"
                  type="button"
                  LeadingIcon={LuRadar}
                  text={m.network_discovery_open()}
                  onClick={() => setDiscoveryOpen(true)}
                  data-testid="network-discovery-open"
                />
              </SettingsItem>

              {isCloudAdopted && <PublicIPCard />}

              {(hasTailscale || hasNetbird) && (
                <SettingsItem
                  title={m.network_vpn_title()}
                  description={m.network_vpn_description()}
                >
                  <LinkButton to="../access" size="SM" theme="light" text={m.network_vpn_open()} />
                </SettingsItem>
              )}

              <div>
                <AutoHeight>
                  {formState.isLoading ? (
                    <GridCard>
                      <div className="p-4">
                        <div className="space-y-4">
                          <div className="h-6 w-1/3 animate-pulse rounded bg-slate-200 dark:bg-slate-700" />
                          <div className="animate-pulse space-y-2">
                            <div className="h-4 w-1/4 rounded bg-slate-200 dark:bg-slate-700" />
                            <div className="h-4 w-1/2 rounded bg-slate-200 dark:bg-slate-700" />
                            <div className="h-4 w-1/3 rounded bg-slate-200 dark:bg-slate-700" />
                            <div className="h-4 w-1/2 rounded bg-slate-200 dark:bg-slate-700" />
                            <div className="h-4 w-1/4 rounded bg-slate-200 dark:bg-slate-700" />
                          </div>
                        </div>
                      </div>
                    </GridCard>
                  ) : ipv4mode === "static" ? (
                    <StaticIpv4Card />
                  ) : ipv4mode === "dhcp" && !!formState.dirtyFields.ipv4_mode ? (
                    <EmptyCard
                      IconElm={LuEthernetPort}
                      headline={m.network_pending_dhcp_mode_change_headline()}
                      description={m.network_pending_dhcp_mode_change_description()}
                    />
                  ) : ipv4mode === "dhcp" ? (
                    <DhcpLeaseCard
                      networkState={networkState}
                      setShowRenewLeaseConfirm={setShowRenewLeaseConfirm}
                    />
                  ) : (
                    <EmptyCard
                      IconElm={LuEthernetPort}
                      headline={m.network_no_information_headline()}
                      description={m.network_no_information_description()}
                    />
                  )}
                </AutoHeight>
              </div>

              {ipv4mode === "dhcp" && !formState.isLoading && (
                <div className="space-y-4">
                  <SettingsItem
                    title={m.network_ipv4_fallback_title()}
                    description={
                      networkState?.ipv4_fallback_active ? (
                        <>
                          {m.network_ipv4_fallback_description()}
                          <span className="mt-0.5 block text-amber-700 dark:text-amber-500">
                            {m.network_ipv4_fallback_active()}
                          </span>
                        </>
                      ) : (
                        m.network_ipv4_fallback_description()
                      )
                    }
                  >
                    <Checkbox {...register("ipv4_fallback_enabled")} />
                  </SettingsItem>
                  {fallbackEnabled && (
                    <>
                      <SettingsItem
                        title={m.network_ipv4_fallback_timeout_title()}
                        description={m.network_ipv4_fallback_timeout_description()}
                      >
                        <InputField
                          size="SM"
                          type="number"
                          min={5}
                          max={600}
                          className="w-24"
                          {...register("ipv4_fallback_timeout", {
                            valueAsNumber: true,
                            validate: (value: number | undefined) =>
                              (value !== undefined && value >= 5 && value <= 600) ||
                              m.network_ipv4_fallback_timeout_invalid(),
                          })}
                          error={formState.errors.ipv4_fallback_timeout?.message}
                        />
                      </SettingsItem>
                      <StaticIpv4Card
                        field="ipv4_fallback"
                        title={m.network_ipv4_fallback_card_title()}
                        description={m.network_ipv4_fallback_card_description()}
                      />
                    </>
                  )}
                </div>
              )}

              {hasIpv6 && (
                <>
                  <SettingsItem
                    title={m.network_ipv6_mode_title()}
                    description={m.network_ipv6_mode_description()}
                  >
                    <SelectMenuBasic
                      size="SM"
                      options={[
                        { value: "disabled", label: m.network_ipv6_mode_disabled() },
                        { value: "slaac", label: m.network_ipv6_mode_slaac() },
                        //{ value: "dhcpv6", label: m.network_ipv6_mode_dhcpv6() },
                        //{ value: "slaac_and_dhcpv6", label: m.network_ipv6_mode_slaac_dhcpv6() },
                        { value: "static", label: m.network_ipv6_mode_static() },
                        { value: "link_local", label: m.network_ipv6_mode_link_local() },
                      ]}
                      {...register("ipv6_mode")}
                    />
                  </SettingsItem>

                  <div className="space-y-4">
                    <AutoHeight>
                      {!networkState ? (
                        <GridCard>
                          <div className="p-4">
                            <div className="space-y-4">
                              <h3 className="text-base font-bold text-slate-900 dark:text-white">
                                {m.network_ipv6_information()}
                              </h3>
                              <div className="animate-pulse space-y-3">
                                <div className="h-4 w-1/3 rounded bg-slate-200 dark:bg-slate-700" />
                                <div className="h-4 w-1/2 rounded bg-slate-200 dark:bg-slate-700" />
                                <div className="h-4 w-1/3 rounded bg-slate-200 dark:bg-slate-700" />
                              </div>
                            </div>
                          </div>
                        </GridCard>
                      ) : ipv6mode === "static" ? (
                        <StaticIpv6Card />
                      ) : (
                        <Ipv6NetworkCard networkState={networkState || undefined} />
                      )}
                    </AutoHeight>
                  </div>
                </>
              )}

              {isLLDPAvailable && (
                <div className="hidden space-y-4">
                  <SettingsItem
                    title={m.network_ll_dp_title()}
                    description={m.network_ll_dp_description()}
                  >
                    <SelectMenuBasic
                      size="SM"
                      options={[
                        { value: "disabled", label: m.network_ll_dp_disabled() },
                        { value: "basic", label: m.network_ll_dp_basic() },
                        { value: "all", label: m.network_ll_dp_all() },
                      ]}
                      {...register("lldp_mode")}
                    />
                  </SettingsItem>
                </div>
              )}

              <div className="animate-fadeInStill animation-duration-300">
                <Button
                  size="SM"
                  theme="primary"
                  disabled={!(formState.isDirty || formState.isSubmitting)}
                  loading={formState.isSubmitting}
                  type="submit"
                  text={formState.isSubmitting ? m.saving() : m.network_save_settings()}
                />
              </div>
            </div>
          </div>
        </form>
      </FormProvider>

      {/* Critical change confirm */}
      <ConfirmDialog
        open={showCriticalSettingsConfirm}
        title={m.network_save_settings_apply_title()}
        variant="warning"
        confirmText={m.network_save_settings_confirm()}
        onConfirm={() => {
          setShowCriticalSettingsConfirm(false);
          if (stagedSettings) onSubmit(stagedSettings);

          // Wait for the close animation to finish before resetting the staged settings
          setTimeout(() => {
            setStagedSettings(null);
            setCriticalChanges([]);
          }, 500);
        }}
        onClose={() => {
          setShowCriticalSettingsConfirm(false);
        }}
        isConfirming={formState.isSubmitting}
        description={
          <div className="space-y-4">
            <div>
              <p className="text-sm leading-relaxed text-slate-700 dark:text-slate-300">
                {m.network_save_settings_confirm_description()}
              </p>
            </div>

            <div className="space-y-2.5">
              <div className="flex items-center justify-between text-[13px] font-medium text-slate-900 dark:text-white">
                {m.network_save_settings_confirm_heading()}
              </div>
              <div className="space-y-2.5">
                {criticalChanges.map((c, idx) => (
                  <div
                    key={idx + c.label}
                    className="flex flex-wrap items-center gap-x-2 gap-y-1 rounded-md border border-slate-800/10 bg-slate-100/50 px-3 py-2 dark:border-slate-300/20 dark:bg-slate-800/50"
                  >
                    <span className="text-xs text-slate-600 dark:text-slate-400">{c.label}</span>
                    <div className="flex items-center gap-2.5">
                      <code className="rounded border border-slate-800/20 bg-slate-50 px-1.5 py-1 font-mono text-xs text-black dark:border-slate-300/20 dark:bg-slate-800 dark:text-slate-100">
                        {c.from || "—"}
                      </code>
                      <svg
                        className="size-3.5 text-slate-400"
                        fill="none"
                        viewBox="0 0 24 24"
                        stroke="currentColor"
                        strokeWidth={2}
                      >
                        <path
                          strokeLinecap="round"
                          strokeLinejoin="round"
                          d="M13 7l5 5m0 0l-5 5m5-5H6"
                        />
                      </svg>
                      <code className="rounded border border-slate-800/20 bg-slate-50 px-1.5 py-1 font-mono text-xs text-black dark:border-slate-300/20 dark:bg-slate-800 dark:text-slate-100">
                        {c.to || "—"}
                      </code>
                    </div>
                  </div>
                ))}
              </div>
            </div>
          </div>
        }
      />

      <NetworkDiscoveryDialog open={discoveryOpen} onClose={() => setDiscoveryOpen(false)} />

      <ConfirmDialog
        open={showRenewLeaseConfirm}
        title={m.dhcp_lease_renew()}
        variant="warning"
        confirmText={m.network_dhcp_lease_renew_confirm()}
        description={
          <p>
            {m.network_dhcp_lease_renew_confirm_description()}
            <br />
            <br />
            {m.network_dhcp_lease_renew_confirm_new_a()}{" "}
            <strong>{m.network_dhcp_lease_renew_confirm_new_b()}</strong>.
          </p>
        }
        onConfirm={() => {
          setShowRenewLeaseConfirm(false);
          onDhcpLeaseRenew();
        }}
        onClose={() => setShowRenewLeaseConfirm(false)}
      />
    </>
  );
}
