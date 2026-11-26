import { useEffect, useState } from "react";
import validator from "validator";
import { LuPlus, LuRadar, LuX } from "react-icons/lu";
import { useFieldArray, useFormContext } from "react-hook-form";
import { cx } from "cva";

import { NetworkSettings } from "@hooks/stores";
import { Button } from "@components/Button";
import { GridCard } from "@components/Card";
import { InputFieldWithLabel } from "@components/InputField";
import { AddressCheck, useNetworkDiscovery } from "@hooks/useNetworkDiscovery";
import { netMaskFromCidr4 } from "@/utils/ip";
import { m } from "@localizations/messages.js";

// The static IPv4 settings, or with field "ipv4_fallback" the static
// fallback of DHCP mode.
export default function StaticIpv4Card({
  field = "ipv4_static",
  title,
  description,
}: {
  field?: "ipv4_static" | "ipv4_fallback";
  title?: string;
  description?: string;
}) {
  const formMethods = useFormContext<NetworkSettings>();
  const { register, formState, watch, setValue } = formMethods;
  const errors = formState.errors[field];

  const { fields, append, remove } = useFieldArray({ name: `${field}.dns` });
  const { checkAddress } = useNetworkDiscovery();
  const [check, setCheck] = useState<
    | { state: "checking" }
    | { state: "done"; result: AddressCheck }
    | { state: "error"; error: string }
    | null
  >(null);

  useEffect(() => {
    if (fields.length === 0) append("");
  }, [append, fields.length]);

  const dns = watch(`${field}.dns`);

  const ipv4StaticAddress = watch(`${field}.address`);

  // A result belongs to the address it was asked for.
  useEffect(() => setCheck(null), [ipv4StaticAddress]);

  const runCheck = () => {
    const address = (ipv4StaticAddress ?? "").split("/")[0];
    if (!validator.isIP(address, 4)) return;
    setCheck({ state: "checking" });
    checkAddress(address).then(
      result => setCheck({ state: "done", result }),
      (error: Error) => setCheck({ state: "error", error: error.message }),
    );
  };
  const hideSubnetMask = ipv4StaticAddress?.includes("/");

  useEffect(() => {
    const parts = ipv4StaticAddress?.split("/", 2);
    if (parts?.length !== 2) return;

    const cidrNotation = parseInt(parts?.[1] ?? "");
    if (isNaN(cidrNotation) || cidrNotation < 0 || cidrNotation > 32) return;

    const mask = netMaskFromCidr4(cidrNotation);
    setValue(`${field}.netmask`, mask);
  }, [ipv4StaticAddress, setValue, field]);

  const ipv4Validation = (value: string) => {
    if (!validator.isIP(value, 4)) return m.network_ipv4_invalid();
    return true;
  };

  const validateIsIPOrCIDR4 = (value: string) => {
    if (!validator.isIP(value, 4) && !validator.isIPRange(value, 4))
      return m.network_ipv4_invalid_cidr();
    return true;
  };

  return (
    <GridCard>
      <div className="animate-fadeIn p-4 text-black opacity-0 animation-duration-500 dark:text-white">
        <div className="space-y-4">
          <div className="space-y-1">
            <h3 className="text-base font-bold text-slate-900 dark:text-white">
              {title ?? m.network_static_ipv4_header()}
            </h3>
            {description && (
              <p className="text-sm text-slate-700 dark:text-slate-300">{description}</p>
            )}
          </div>

          <div
            className={cx(
              "grid grid-cols-1 gap-4",
              hideSubnetMask ? "md:grid-cols-1" : "md:grid-cols-2",
            )}
          >
            <InputFieldWithLabel
              label={m.network_ipv4_address()}
              type="text"
              size="SM"
              placeholder="192.168.1.100"
              {...register(`${field}.address`, {
                validate: (value: string | undefined) => validateIsIPOrCIDR4(value ?? ""),
              })}
              error={errors?.address?.message}
            />

            {!hideSubnetMask && (
              <InputFieldWithLabel
                label={m.network_ipv4_netmask()}
                type="text"
                size="SM"
                placeholder="255.255.255.0"
                {...register(`${field}.netmask`, {
                  validate: (value: string | undefined) => ipv4Validation(value ?? ""),
                })}
                error={errors?.netmask?.message}
              />
            )}
          </div>

          <InputFieldWithLabel
            label={m.network_ipv4_gateway()}
            type="text"
            size="SM"
            placeholder="192.168.1.1"
            {...register(`${field}.gateway`, {
              validate: (value: string | undefined) => !value || ipv4Validation(value),
            })}
            error={errors?.gateway?.message}
          />

          <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
            <Button
              size="XS"
              theme="light"
              type="button"
              LeadingIcon={LuRadar}
              text={m.network_check_address()}
              loading={check?.state === "checking"}
              disabled={!validator.isIP((ipv4StaticAddress ?? "").split("/")[0], 4)}
              onClick={runCheck}
            />
            {check?.state === "done" && (
              <span
                className={cx(
                  "text-xs",
                  check.result.in_use
                    ? "text-red-600 dark:text-red-400"
                    : "text-green-700 dark:text-green-400",
                )}
              >
                {check.result.own
                  ? m.network_check_address_own()
                  : check.result.in_use
                    ? m.network_address_in_use({ mac: check.result.mac ?? "" })
                    : m.network_check_address_free()}
              </span>
            )}
            {check?.state === "error" && (
              <span className="text-xs text-red-600 dark:text-red-400">{check.error}</span>
            )}
          </div>

          {/* DNS server fields */}
          <div className="space-y-4">
            {fields.map((dns, index) => {
              return (
                <div key={dns.id}>
                  <div className="flex items-start gap-x-2">
                    <div className="flex-1">
                      <InputFieldWithLabel
                        label={index === 0 ? m.network_ipv4_dns() : null}
                        type="text"
                        size="SM"
                        placeholder="1.1.1.1"
                        {...register(`${field}.dns.${index}`, {
                          validate: (value: string | undefined) => !value || ipv4Validation(value),
                        })}
                        error={errors?.dns?.[index]?.message}
                      />
                    </div>
                    {index > 0 && (
                      <div className="flex-shrink-0">
                        <Button
                          size="SM"
                          theme="light"
                          type="button"
                          onClick={() => remove(index)}
                          LeadingIcon={LuX}
                        />
                      </div>
                    )}
                  </div>
                </div>
              );
            })}
          </div>

          <Button
            size="SM"
            theme="light"
            onClick={() => append("", { shouldFocus: true })}
            LeadingIcon={LuPlus}
            type="button"
            text={m.network_settings_add_dns()}
            disabled={dns?.[0] === ""}
          />
        </div>
      </div>
    </GridCard>
  );
}
