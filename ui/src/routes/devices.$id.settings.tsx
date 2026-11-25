import React, { useEffect, useRef, useState } from "react";
import { NavLink, Outlet, useLocation } from "react-router";
import { useResizeObserver } from "usehooks-ts";
import {
  LuSettings,
  LuMouse,
  LuKeyboard,
  LuVideo,
  LuVolume2,
  LuCpu,
  LuShieldCheck,
  LuWrench,
  LuArrowLeft,
  LuPalette,
  LuCommand,
  LuNetwork,
  LuRadio,
  LuMonitor,
  LuChevronDown,
  LuChevronRight,
  LuUsb,
  LuCamera,
  LuMonitorSmartphone,
} from "react-icons/lu";

import { cx } from "@/cva.config";
import { useUiStore, useFailsafeModeStore, useCapability } from "@hooks/stores";
import Card from "@components/Card";
import { FailsafeModeBanner } from "@components/FailSafeModeBanner";
import { LinkButton } from "@components/Button";
import { m } from "@localizations/messages.js";

/* TODO: Migrate to using URLs instead of the global state. To simplify the refactoring, we'll keep the global state for now. */
export default function SettingsRoute() {
  const location = useLocation();
  const { setDisableVideoFocusTrap } = useUiStore();
  const scrollContainerRef = useRef<HTMLDivElement>(null);
  const [showLeftGradient, setShowLeftGradient] = useState(false);
  const [showRightGradient, setShowRightGradient] = useState(false);
  const { width = 0 } = useResizeObserver({
    ref: scrollContainerRef as React.RefObject<HTMLDivElement>,
  });
  const { isFailsafeMode, reason: failsafeReason } = useFailsafeModeStore();
  const isNativeDisabled = isFailsafeMode && failsafeReason === "native";
  const osServices = useCapability("os_services");
  const inHardware = /\/settings\/hardware(\/|$)/.test(location.pathname);
  const [hardwareOpen, setHardwareOpen] = useState(inHardware);
  useEffect(() => {
    if (inHardware) setHardwareOpen(true);
  }, [inHardware]);
  const hardwarePages = [
    { to: "hardware", end: true, icon: LuMonitorSmartphone, label: m.settings_hardware_display() },
    { to: "hardware/usb", end: false, icon: LuUsb, label: m.settings_hardware_usb() },
    { to: "hardware/camera", end: false, icon: LuCamera, label: m.settings_hardware_camera() },
  ];

  // Handle scroll position to show/hide gradients
  const handleScroll = () => {
    if (scrollContainerRef.current) {
      const { scrollLeft, scrollWidth, clientWidth } = scrollContainerRef.current;
      // Show left gradient only if scrolled to the right
      setShowLeftGradient(scrollLeft > 0);
      // Show right gradient only if there's more content to scroll to the right
      setShowRightGradient(scrollLeft < scrollWidth - clientWidth - 1); // -1 for rounding errors
    }
  };

  useEffect(() => {
    // Check initial scroll position
    handleScroll();

    // Add scroll event listener to the container
    const scrollContainer = scrollContainerRef.current;
    if (scrollContainer) {
      scrollContainer.addEventListener("scroll", handleScroll);
    }

    return () => {
      // Clean up event listener
      if (scrollContainer) {
        scrollContainer.removeEventListener("scroll", handleScroll);
      }
    };
  }, [width]);

  useEffect(() => {
    setTimeout(() => {
      setDisableVideoFocusTrap(true);
    }, 500);

    return () => {
      setDisableVideoFocusTrap(false);
    };
  }, [setDisableVideoFocusTrap]);

  return (
    <div className="pointer-events-auto relative mx-auto max-w-4xl translate-x-0 transform text-left dark:text-white">
      <div className="h-full">
        <div className="w-full space-y-4 gap-x-8 gap-y-4 md:grid md:grid-cols-8 md:space-y-0">
          <div className="w-full space-y-4 select-none md:col-span-2">
            <Card className="flex w-full gap-x-4 overflow-hidden p-2 md:flex-col dark:bg-slate-800">
              <div className="md:hidden">
                <LinkButton
                  to=".."
                  size="SM"
                  theme="blank"
                  text={m.settings_back_to_kvm()}
                  LeadingIcon={LuArrowLeft}
                  textAlign="left"
                />
              </div>
              <div className="hidden md:block">
                <LinkButton
                  to=".."
                  size="SM"
                  theme="blank"
                  text={m.settings_back_to_kvm()}
                  LeadingIcon={LuArrowLeft}
                  textAlign="left"
                  fullWidth
                />
              </div>
            </Card>
            <Card className="relative overflow-hidden">
              {/* Gradient overlay for left side - only visible on mobile when scrolled */}
              <div
                className={cx(
                  "pointer-events-none absolute inset-y-0 left-0 z-10 w-8 bg-linear-to-r from-white to-transparent transition-opacity duration-300 ease-in-out md:hidden dark:from-slate-900",
                  {
                    "opacity-0": !showLeftGradient,
                    "opacity-100": showLeftGradient,
                  },
                )}
              ></div>
              {/* Gradient overlay for right side - only visible on mobile when there's more content */}
              <div
                className={cx(
                  "pointer-events-none absolute inset-y-0 right-0 z-10 w-8 bg-linear-to-l from-white to-transparent transition duration-300 ease-in-out md:hidden dark:from-slate-900",
                  {
                    "opacity-0": !showRightGradient,
                    "opacity-100": showRightGradient,
                  },
                )}
              ></div>
              <div
                ref={scrollContainerRef}
                className="hide-scrollbar relative flex w-full gap-x-4 overflow-x-auto p-2 whitespace-nowrap md:flex-col md:overflow-visible md:whitespace-normal dark:bg-slate-800"
              >
                <div className="shrink-0">
                  <NavLink to="general" className={({ isActive }) => (isActive ? "active" : "")}>
                    <div className="flex items-center gap-x-2 rounded-md px-2.5 py-2.5 text-sm transition-colors hover:bg-slate-100 in-[.active]:bg-blue-50 in-[.active]:text-blue-700! md:in-[.active]:bg-transparent dark:hover:bg-slate-700 dark:in-[.active]:bg-blue-900 dark:in-[.active]:text-blue-200! dark:md:in-[.active]:bg-transparent">
                      <LuSettings className="h-4 w-4 shrink-0" />
                      <h1>{m.settings_general()}</h1>
                    </div>
                  </NavLink>
                </div>
                <div className="shrink-0">
                  <NavLink to="mouse" className={({ isActive }) => (isActive ? "active" : "")}>
                    <div className="flex items-center gap-x-2 rounded-md px-2.5 py-2.5 text-sm transition-colors hover:bg-slate-100 in-[.active]:bg-blue-50 in-[.active]:text-blue-700! md:in-[.active]:bg-transparent dark:hover:bg-slate-700 dark:in-[.active]:bg-blue-900 dark:in-[.active]:text-blue-200! dark:md:in-[.active]:bg-transparent">
                      <LuMouse className="h-4 w-4 shrink-0" />
                      <h1>{m.settings_mouse()}</h1>
                    </div>
                  </NavLink>
                </div>
                <div className="shrink-0">
                  <NavLink to="keyboard" className={({ isActive }) => (isActive ? "active" : "")}>
                    <div className="flex items-center gap-x-2 rounded-md px-2.5 py-2.5 text-sm transition-colors hover:bg-slate-100 in-[.active]:bg-blue-50 in-[.active]:text-blue-700! md:in-[.active]:bg-transparent dark:hover:bg-slate-700 dark:in-[.active]:bg-blue-900 dark:in-[.active]:text-blue-200! dark:md:in-[.active]:bg-transparent">
                      <LuKeyboard className="h-4 w-4 shrink-0" />
                      <h1>{m.settings_keyboard()}</h1>
                    </div>
                  </NavLink>
                </div>
                <div
                  className={cx("shrink-0", {
                    "cursor-not-allowed opacity-50": isNativeDisabled,
                  })}
                >
                  <NavLink
                    to="video"
                    className={({ isActive }) =>
                      cx(isActive ? "active" : "", {
                        "pointer-events-none": isNativeDisabled,
                      })
                    }
                  >
                    <div className="flex items-center gap-x-2 rounded-md px-2.5 py-2.5 text-sm transition-colors hover:bg-slate-100 in-[.active]:bg-blue-50 in-[.active]:text-blue-700! md:in-[.active]:bg-transparent dark:hover:bg-slate-700 dark:in-[.active]:bg-blue-900 dark:in-[.active]:text-blue-200! dark:md:in-[.active]:bg-transparent">
                      <LuVideo className="h-4 w-4 shrink-0" />
                      <h1>{m.settings_video()}</h1>
                    </div>
                  </NavLink>
                </div>
                <div className="shrink-0">
                  <NavLink to="audio" className={({ isActive }) => (isActive ? "active" : "")}>
                    <div className="flex items-center gap-x-2 rounded-md px-2.5 py-2.5 text-sm transition-colors hover:bg-slate-100 in-[.active]:bg-blue-50 in-[.active]:text-blue-700! md:in-[.active]:bg-transparent dark:hover:bg-slate-700 dark:in-[.active]:bg-blue-900 dark:in-[.active]:text-blue-200! dark:md:in-[.active]:bg-transparent">
                      <LuVolume2 className="h-4 w-4 shrink-0" />
                      <h1>{m.settings_audio()}</h1>
                    </div>
                  </NavLink>
                </div>
                {osServices && (
                  <div className="shrink-0">
                    <NavLink
                      to="os-services"
                      className={({ isActive }) => (isActive ? "active" : "")}
                    >
                      <div className="flex items-center gap-x-2 rounded-md px-2.5 py-2.5 text-sm transition-colors hover:bg-slate-100 in-[.active]:bg-blue-50 in-[.active]:text-blue-700! md:in-[.active]:bg-transparent dark:hover:bg-slate-700 dark:in-[.active]:bg-blue-900 dark:in-[.active]:text-blue-200! dark:md:in-[.active]:bg-transparent">
                        <LuMonitor className="h-4 w-4 shrink-0" />
                        <h1>{m.settings_os_services()}</h1>
                      </div>
                    </NavLink>
                  </div>
                )}
                <div
                  className={cx("shrink-0", {
                    "cursor-not-allowed opacity-50": isNativeDisabled,
                  })}
                >
                  <div className="flex items-center">
                    <NavLink
                      to="hardware"
                      end={false}
                      onClick={() => setHardwareOpen(true)}
                      className={({ isActive }) =>
                        cx("min-w-0 flex-1", isActive ? "active" : "", {
                          "pointer-events-none": isNativeDisabled,
                        })
                      }
                    >
                      <div className="flex items-center gap-x-2 rounded-md px-2.5 py-2.5 text-sm transition-colors hover:bg-slate-100 in-[.active]:bg-blue-50 in-[.active]:text-blue-700! md:in-[.active]:bg-transparent dark:hover:bg-slate-700 dark:in-[.active]:bg-blue-900 dark:in-[.active]:text-blue-200! dark:md:in-[.active]:bg-transparent">
                        <LuCpu className="h-4 w-4 shrink-0" />
                        <h1>{m.settings_hardware()}</h1>
                      </div>
                    </NavLink>
                    <button
                      type="button"
                      aria-expanded={hardwareOpen}
                      aria-label={m.settings_hardware_toggle()}
                      disabled={isNativeDisabled}
                      onClick={() => setHardwareOpen(open => !open)}
                      className="rounded-md p-2 text-slate-500 hover:bg-slate-100 dark:text-slate-400 dark:hover:bg-slate-700"
                    >
                      {hardwareOpen ? (
                        <LuChevronDown className="h-4 w-4" />
                      ) : (
                        <LuChevronRight className="h-4 w-4" />
                      )}
                    </button>
                  </div>
                </div>
                {hardwareOpen &&
                  hardwarePages.map(page => (
                    <div
                      key={page.to}
                      className={cx(
                        "shrink-0 md:ml-4 md:border-l md:border-slate-800/10 md:pl-1 dark:md:border-slate-300/20",
                        {
                          "cursor-not-allowed opacity-50": isNativeDisabled,
                        },
                      )}
                    >
                      <NavLink
                        to={page.to}
                        end={page.end}
                        className={({ isActive }) =>
                          cx(isActive ? "active" : "", { "pointer-events-none": isNativeDisabled })
                        }
                      >
                        <div className="flex items-center gap-x-2 rounded-md px-2.5 py-2 text-[13px] transition-colors hover:bg-slate-100 in-[.active]:bg-blue-50 in-[.active]:text-blue-700! md:in-[.active]:bg-transparent dark:hover:bg-slate-700 dark:in-[.active]:bg-blue-900 dark:in-[.active]:text-blue-200! dark:md:in-[.active]:bg-transparent">
                          <page.icon className="h-3.5 w-3.5 shrink-0" />
                          <span>{page.label}</span>
                        </div>
                      </NavLink>
                    </div>
                  ))}
                <div className="shrink-0">
                  <NavLink to="access" className={({ isActive }) => (isActive ? "active" : "")}>
                    <div className="flex items-center gap-x-2 rounded-md px-2.5 py-2.5 text-sm transition-colors hover:bg-slate-100 in-[.active]:bg-blue-50 in-[.active]:text-blue-700! md:in-[.active]:bg-transparent dark:hover:bg-slate-700 dark:in-[.active]:bg-blue-900 dark:in-[.active]:text-blue-200! dark:md:in-[.active]:bg-transparent">
                      <LuShieldCheck className="h-4 w-4 shrink-0" />
                      <h1>{m.settings_access()}</h1>
                    </div>
                  </NavLink>
                </div>
                <div className="shrink-0">
                  <NavLink to="appearance" className={({ isActive }) => (isActive ? "active" : "")}>
                    <div className="flex items-center gap-x-2 rounded-md px-2.5 py-2.5 text-sm transition-colors hover:bg-slate-100 in-[.active]:bg-blue-50 in-[.active]:text-blue-700! md:in-[.active]:bg-transparent dark:hover:bg-slate-700 dark:in-[.active]:bg-blue-900 dark:in-[.active]:text-blue-200! dark:md:in-[.active]:bg-transparent">
                      <LuPalette className="h-4 w-4 shrink-0" />
                      <h1>{m.settings_appearance()}</h1>
                    </div>
                  </NavLink>
                </div>
                <div className="shrink-0">
                  <NavLink to="macros" className={({ isActive }) => (isActive ? "active" : "")}>
                    <div className="flex items-center gap-x-2 rounded-md px-2.5 py-2.5 text-sm transition-colors hover:bg-slate-100 in-[.active]:bg-blue-50 in-[.active]:text-blue-700! md:in-[.active]:bg-transparent dark:hover:bg-slate-700 dark:in-[.active]:bg-blue-900 dark:in-[.active]:text-blue-200! dark:md:in-[.active]:bg-transparent">
                      <LuCommand className="h-4 w-4 shrink-0" />
                      <h1>{m.settings_keyboard_macros()}</h1>
                    </div>
                  </NavLink>
                </div>
                <div className="shrink-0">
                  <NavLink to="network" className={({ isActive }) => (isActive ? "active" : "")}>
                    <div className="flex items-center gap-x-2 rounded-md px-2.5 py-2.5 text-sm transition-colors hover:bg-slate-100 in-[.active]:bg-blue-50 in-[.active]:text-blue-700! md:in-[.active]:bg-transparent dark:hover:bg-slate-700 dark:in-[.active]:bg-blue-900 dark:in-[.active]:text-blue-200! dark:md:in-[.active]:bg-transparent">
                      <LuNetwork className="h-4 w-4 shrink-0" />
                      <h1>{m.settings_network()}</h1>
                    </div>
                  </NavLink>
                </div>
                <div className="shrink-0">
                  <NavLink to="mqtt" className={({ isActive }) => (isActive ? "active" : "")}>
                    <div className="flex items-center gap-x-2 rounded-md px-2.5 py-2.5 text-sm transition-colors hover:bg-slate-100 in-[.active]:bg-blue-50 in-[.active]:text-blue-700! md:in-[.active]:bg-transparent dark:hover:bg-slate-700 dark:in-[.active]:bg-blue-900 dark:in-[.active]:text-blue-200! dark:md:in-[.active]:bg-transparent">
                      <LuRadio className="h-4 w-4 shrink-0" />
                      <h1>{m.settings_mqtt()}</h1>
                    </div>
                  </NavLink>
                </div>
                <div className="shrink-0">
                  <NavLink to="advanced" className={({ isActive }) => (isActive ? "active" : "")}>
                    <div className="flex items-center gap-x-2 rounded-md px-2.5 py-2.5 text-sm transition-colors hover:bg-slate-100 in-[.active]:bg-blue-50 in-[.active]:text-blue-700! md:in-[.active]:bg-transparent dark:hover:bg-slate-700 dark:in-[.active]:bg-blue-900 dark:in-[.active]:text-blue-200! dark:md:in-[.active]:bg-transparent">
                      <LuWrench className="h-4 w-4 shrink-0" />
                      <h1>{m.settings_advanced()}</h1>
                    </div>
                  </NavLink>
                </div>
              </div>
            </Card>
          </div>
          <div className="w-full space-y-4 md:col-span-6">
            {isFailsafeMode && failsafeReason && <FailsafeModeBanner reason={failsafeReason} />}
            <Card className="dark:bg-slate-800">
              <div
                className="space-y-4 px-8 py-6"
                style={{ animationDuration: "0.7s" }}
                key={location.pathname} // This is a workaround to force the animation to run when the route changes
              >
                <Outlet />
              </div>
            </Card>
            {/* </AutoHeight> */}
          </div>
        </div>
      </div>
    </div>
  );
}
