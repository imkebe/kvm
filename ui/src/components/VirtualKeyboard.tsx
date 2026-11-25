import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { ChevronDownIcon } from "@heroicons/react/16/solid";
import { AnimatePresence, motion } from "framer-motion";
import { KeyboardReact as Keyboard } from "react-simple-keyboard";
import { LuAppWindow, LuKeyboard, LuPanelBottom } from "react-icons/lu";

import "react-simple-keyboard/build/css/index.css";
import DetachIconRaw from "@/assets/detach-icon.svg";
import { cx } from "@/cva.config";
import { useHidStore, useUiStore } from "@hooks/stores";
import useKeyboard from "@hooks/useKeyboard";
import useKeyboardLayout from "@hooks/useKeyboardLayout";
import { Button, LinkButton } from "@components/Button";
import Card from "@components/Card";
import { decodeModifiers, keys, latchingKeys, modifiers } from "@/keyboardMappings";
import { m } from "@localizations/messages.js";
import notifications from "@/notifications";

export const DetachIcon = ({ className }: { className?: string }) => {
  return <img src={DetachIconRaw} alt="Detach Icon" className={className} />;
};

interface Placement {
  x: number;
  y: number;
  width: number;
}

// v2: placements saved before 2026-11-24 could hold the 480 px a keyboard
// shrank to after its own window (it reported 0 x 0 off the page).
const PLACEMENT_KEY = "jetkvm.virtualKeyboard.detached.v2";
const DEFAULT_DETACHED_WIDTH = 900;
const MIN_DETACHED_WIDTH = 480;
// How much of the title bar stays on screen.
const KEEP_VISIBLE = 96;
const TITLE_HEIGHT = 48;

function readPlacement(): Placement | null {
  try {
    const value = JSON.parse(localStorage.getItem(PLACEMENT_KEY) ?? "null") as Placement | null;
    if (value && [value.x, value.y, value.width].every(Number.isFinite)) {
      return clampPlacement(value);
    }
  } catch {
    // Storage unavailable or unreadable: place it afresh.
  }
  return null;
}

function savePlacement(placement: Placement) {
  try {
    localStorage.setItem(PLACEMENT_KEY, JSON.stringify(placement));
  } catch {
    // Not remembered; it still works.
  }
}

// Anywhere, even partly off screen, as long as part of the title bar can
// still be grabbed; the bottom row stays on screen when the keyboard fits
// (height: the keyboard's height, when known).
function clampPlacement(placement: Placement, height = 0): Placement {
  const width = Math.max(MIN_DETACHED_WIDTH, Math.min(placement.width, window.innerWidth));
  const maxY =
    height > 0 && height <= window.innerHeight
      ? window.innerHeight - height
      : window.innerHeight - TITLE_HEIGHT;
  return {
    width,
    x: Math.min(window.innerWidth - KEEP_VISIBLE, Math.max(KEEP_VISIBLE - width, placement.x)),
    y: Math.min(maxY, Math.max(0, placement.y)),
  };
}

// The keyboard library checks its element with instanceof HTMLDivElement,
// which fails for elements of another window (the keyboard's own window).
// Elements of other windows of this page count too.
let foreignDivsAllowed = false;
function allowForeignDivs() {
  if (foreignDivsAllowed) return;
  foreignDivsAllowed = true;
  const isInstance = Function.prototype[Symbol.hasInstance];
  Object.defineProperty(HTMLDivElement, Symbol.hasInstance, {
    configurable: true,
    value(this: typeof HTMLDivElement, value: unknown) {
      if (isInstance.call(this, value)) return true;
      const view = (value as Node | null)?.ownerDocument?.defaultView as
        | (Window & typeof globalThis)
        | null;
      return !!view && view !== window && isInstance.call(view.HTMLDivElement, value);
    },
  });
}

const WINDOW_NAME = "jetkvm-virtual-keyboard";

// Opens the keyboard's own window with this page's styles; null when the
// browser blocked it.
function openKeyboardWindow(): { win: Window; container: HTMLElement } | null {
  const win = window.open("", WINDOW_NAME, "popup,width=1000,height=420");
  if (!win) return null;
  const doc = win.document;
  doc.title = m.virtual_keyboard_header();
  doc.head.innerHTML = "";
  document.head.querySelectorAll('link[rel="stylesheet"], style').forEach(node => {
    const copy = node.cloneNode(true) as HTMLElement;
    // Absolute, so the new window loads the same files.
    if (node instanceof HTMLLinkElement) copy.setAttribute("href", node.href);
    doc.head.appendChild(copy);
  });
  doc.documentElement.className = document.documentElement.className;
  doc.body.className = "m-0 overflow-auto bg-white dark:bg-slate-900";
  doc.body.innerHTML = "";
  const container = doc.createElement("div");
  doc.body.appendChild(container);
  return { win, container };
}

function KeyboardWrapper() {
  const keyboardRef = useRef<HTMLDivElement>(null);
  const { isAttachedVirtualKeyboardVisible, setAttachedVirtualKeyboardVisibility } = useUiStore();
  const { keyboardLedState, keysDownState, isVirtualKeyboardEnabled, setVirtualKeyboardEnabled } =
    useHidStore();
  const { handleKeyPress, executeMacro } = useKeyboard();
  const { selectedKeyboard } = useKeyboardLayout();

  // Detached, the keyboard floats where its title bar is dragged to, at a
  // width the user can change; both are remembered in this browser.
  const [placement, setPlacement] = useState<Placement | null>(readPlacement);
  const drag = useRef<{ dx: number; dy: number; pointerId: number } | null>(null);
  const heightRef = useRef(0);

  // The keyboard in its own browser window (rendered from this page).
  const [keyboardWindow, setKeyboardWindow] = useState<{
    win: Window;
    container: HTMLElement;
  } | null>(null);

  const closeKeyboardWindow = useCallback(() => {
    setKeyboardWindow(current => {
      if (current && !current.win.closed) current.win.close();
      return null;
    });
  }, []);

  const openWindow = useCallback(() => {
    allowForeignDivs();
    const opened = openKeyboardWindow();
    if (!opened) {
      notifications.error(m.virtual_keyboard_window_blocked());
      return;
    }
    setKeyboardWindow(opened);
  }, []);

  // Closing the window brings the keyboard back to this page; leaving the
  // page closes the window.
  useEffect(() => {
    if (!keyboardWindow) return;
    const timer = window.setInterval(() => {
      if (keyboardWindow.win.closed) setKeyboardWindow(null);
    }, 500);
    const onLeave = () => keyboardWindow.win.close();
    window.addEventListener("pagehide", onLeave);
    return () => {
      window.clearInterval(timer);
      window.removeEventListener("pagehide", onLeave);
    };
  }, [keyboardWindow]);

  // Hiding the keyboard closes its window.
  useEffect(() => {
    if (!isVirtualKeyboardEnabled) closeKeyboardWindow();
  }, [isVirtualKeyboardEnabled, closeKeyboardWindow]);

  useEffect(() => () => closeKeyboardWindow(), [closeKeyboardWindow]);

  const keyDisplayMap = useMemo(() => {
    return selectedKeyboard.keyDisplayMap;
  }, [selectedKeyboard]);

  const virtualKeyboard = useMemo(() => {
    return selectedKeyboard.virtualKeyboard;
  }, [selectedKeyboard]);

  const { isShiftActive } = useMemo(() => {
    return decodeModifiers(keysDownState.modifier);
  }, [keysDownState]);

  const isCapsLockActive = useMemo(() => {
    return keyboardLedState.caps_lock;
  }, [keyboardLedState]);

  const mainLayoutName = useMemo(() => {
    // if you have the CapsLock "latched", then the shift state is inverted
    const effectiveShift = isCapsLockActive ? false === isShiftActive : isShiftActive;
    return effectiveShift ? "shift" : "default";
  }, [isCapsLockActive, isShiftActive]);

  const keyNamesForDownKeys = useMemo(() => {
    const activeModifierMask = keysDownState.modifier || 0;
    const modifierNames = Object.entries(modifiers)
      .filter(([_, mask]) => (activeModifierMask & mask) !== 0)
      .map(([name, _]) => name);

    const keysDown = keysDownState.keys || [];
    const keyNames = Object.entries(keys)
      .filter(([_, value]) => keysDown.includes(value))
      .map(([name, _]) => name);

    return [...modifierNames, ...keyNames, " "]; // we have to have at least one space to avoid keyboard whining
  }, [keysDownState]);

  // Where a newly detached keyboard goes: centred near the bottom.
  useEffect(() => {
    if (isAttachedVirtualKeyboardVisible || !isVirtualKeyboardEnabled || placement) return;
    const width = Math.min(DEFAULT_DETACHED_WIDTH, window.innerWidth - 16);
    setPlacement(
      clampPlacement({
        x: (window.innerWidth - width) / 2,
        y: window.innerHeight - 380,
        width,
      }),
    );
  }, [isAttachedVirtualKeyboardVisible, isVirtualKeyboardEnabled, placement]);

  // A smaller window keeps the title bar reachable.
  useEffect(() => {
    const onResize = () =>
      setPlacement(current => current && clampPlacement(current, heightRef.current));
    window.addEventListener("resize", onResize);
    return () => window.removeEventListener("resize", onResize);
  }, []);

  // The native resize handle changes the width; it is kept for next time.
  useEffect(() => {
    const element = keyboardRef.current;
    if (isAttachedVirtualKeyboardVisible || !element) return;
    const observer = new ResizeObserver(() => {
      // Taken off the page (the keyboard moved to its own window) the element
      // reports 0 x 0; that is not a width to keep.
      if (!element.isConnected) return;
      const rect = element.getBoundingClientRect();
      if (rect.width === 0 || rect.height === 0) return;
      const width = Math.round(rect.width);
      // A taller keyboard (narrower, keys stacked) moves up to stay whole.
      const heightChanged = Math.abs(heightRef.current - rect.height) >= 2;
      heightRef.current = rect.height;
      setPlacement(current => {
        if (!current) return current;
        if (Math.abs(current.width - width) < 2) {
          return heightChanged ? clampPlacement(current, rect.height) : current;
        }
        const next = clampPlacement({ ...current, width }, rect.height);
        savePlacement(next);
        return next;
      });
    });
    observer.observe(element);
    return () => observer.disconnect();
    // keyboardWindow: back from its own window, the page has a new element.
  }, [isAttachedVirtualKeyboardVisible, isVirtualKeyboardEnabled, keyboardWindow]);

  const onHandlePointerDown = useCallback(
    (e: React.PointerEvent<HTMLDivElement>) => {
      if (isAttachedVirtualKeyboardVisible || !placement) return;
      if ((e.target as HTMLElement).closest("button, a")) return;
      e.preventDefault();
      e.currentTarget.setPointerCapture(e.pointerId);
      drag.current = {
        dx: e.clientX - placement.x,
        dy: e.clientY - placement.y,
        pointerId: e.pointerId,
      };
    },
    [isAttachedVirtualKeyboardVisible, placement],
  );

  const onHandlePointerMove = useCallback((e: React.PointerEvent<HTMLDivElement>) => {
    const current = drag.current;
    if (!current || current.pointerId !== e.pointerId) return;
    setPlacement(
      placement =>
        placement &&
        clampPlacement(
          { ...placement, x: e.clientX - current.dx, y: e.clientY - current.dy },
          heightRef.current,
        ),
    );
  }, []);

  const onHandlePointerUp = useCallback((e: React.PointerEvent<HTMLDivElement>) => {
    if (drag.current?.pointerId !== e.pointerId) return;
    drag.current = null;
    e.currentTarget.releasePointerCapture(e.pointerId);
    setPlacement(placement => {
      if (placement) savePlacement(placement);
      return placement;
    });
  }, []);

  const onKeyUp = useCallback(async (_: string, e: MouseEvent | undefined) => {
    e?.preventDefault();
    e?.stopPropagation();
  }, []);

  const onKeyDown = useCallback(
    async (key: string, e: MouseEvent | undefined) => {
      e?.preventDefault();
      e?.stopPropagation();

      // handle the fake key-macros we have defined for common combinations
      if (key === "CtrlAltDelete") {
        await executeMacro([
          { keys: ["Delete"], modifiers: ["ControlLeft", "AltLeft"], delay: 100 },
        ]);
        return;
      }

      if (key === "AltMetaEscape") {
        await executeMacro([{ keys: ["Escape"], modifiers: ["AltLeft", "MetaLeft"], delay: 100 }]);
        return;
      }

      if (key === "CtrlAltBackspace") {
        await executeMacro([
          { keys: ["Backspace"], modifiers: ["ControlLeft", "AltLeft"], delay: 100 },
        ]);
        return;
      }

      // if they press any of the latching keys, we send a keypress down event and the release it automatically (on timer)
      if (latchingKeys.includes(key)) {
        console.debug(`Latching key pressed: ${key} sending down and delayed up pair`);
        void handleKeyPress(keys[key], true);
        setTimeout(() => void handleKeyPress(keys[key], false), 100);
        return;
      }

      // if they press any of the dynamic keys, we send a keypress down event but we don't release it until they click it again
      if (Object.keys(modifiers).includes(key)) {
        const currentlyDown = keyNamesForDownKeys.includes(key);
        console.debug(
          `Dynamic key pressed: ${key} was currently down: ${currentlyDown}, toggling state`,
        );
        void handleKeyPress(keys[key], !currentlyDown);
        return;
      }

      // otherwise, just treat it as a down+up pair
      const cleanKey = key.replace(/[()]/g, "");
      console.debug(`Regular key pressed: ${cleanKey} sending down and up pair`);
      void handleKeyPress(keys[cleanKey], true);
      setTimeout(() => void handleKeyPress(keys[cleanKey], false), 50);
    },
    [executeMacro, handleKeyPress, keyNamesForDownKeys],
  );

  // The card itself: in the page (attached or floating) or in its own window.
  const renderCard = (inWindow: boolean) => {
    const detached = inWindow || !isAttachedVirtualKeyboardVisible;
    const floating = !inWindow && !isAttachedVirtualKeyboardVisible;
    return (
      <Card
        className={cx("overflow-hidden", {
          "rounded-none": !floating,
          "keyboard-detached": detached,
        })}
      >
        <div
          className={cx(
            "relative flex items-center justify-center border-b border-b-slate-800/30 bg-white px-2 py-4 dark:border-b-slate-300/20 dark:bg-slate-800",
            floating && "cursor-move touch-none",
          )}
          title={floating ? m.virtual_keyboard_drag() : undefined}
          onPointerDown={floating ? onHandlePointerDown : undefined}
          onPointerMove={floating ? onHandlePointerMove : undefined}
          onPointerUp={floating ? onHandlePointerUp : undefined}
          onPointerCancel={floating ? onHandlePointerUp : undefined}
        >
          <div className="absolute left-2 flex items-center gap-x-2">
            {inWindow ? (
              <Button
                size="XS"
                theme="light"
                text={m.virtual_keyboard_back_to_page()}
                LeadingIcon={LuPanelBottom}
                onClick={closeKeyboardWindow}
              />
            ) : isAttachedVirtualKeyboardVisible ? (
              <Button
                size="XS"
                theme="light"
                text={m.detach()}
                onClick={() => setAttachedVirtualKeyboardVisibility(false)}
              />
            ) : (
              <>
                <Button
                  size="XS"
                  theme="light"
                  text={m.attach()}
                  onClick={() => setAttachedVirtualKeyboardVisibility(true)}
                />
                <Button
                  size="XS"
                  theme="light"
                  text={m.virtual_keyboard_new_window()}
                  LeadingIcon={LuAppWindow}
                  onClick={openWindow}
                />
              </>
            )}
          </div>
          <h2 className="self-center font-sans text-sm leading-none font-medium text-slate-700 select-none dark:text-slate-300">
            {m.virtual_keyboard_header()}
          </h2>
          <div className="absolute right-2 flex items-center gap-x-2">
            {!inWindow && (
              <div className="hidden items-center gap-x-2 md:flex">
                <LinkButton
                  size="XS"
                  to="settings/keyboard"
                  theme="light"
                  text={selectedKeyboard.name}
                  LeadingIcon={LuKeyboard}
                />
                <div className="h-[20px] w-px bg-slate-800/20 dark:bg-slate-200/20" />
              </div>
            )}

            <Button
              size="XS"
              theme="light"
              text={m.hide()}
              LeadingIcon={ChevronDownIcon}
              onClick={() => setVirtualKeyboardEnabled(false)}
            />
          </div>
        </div>

        <div className="kb-area">
          <div
            className={cx(
              "kb-row flex flex-col bg-blue-50/80 dark:bg-slate-700",
              !detached && "md:flex-row",
            )}
          >
            <Keyboard
              baseClass="simple-keyboard-main"
              layoutName={mainLayoutName}
              onKeyPress={onKeyDown}
              onKeyReleased={onKeyUp}
              buttonTheme={[
                {
                  class: "combination-key",
                  buttons: "CtrlAltDelete AltMetaEscape CtrlAltBackspace",
                },
                {
                  class: "down-key",
                  buttons: keyNamesForDownKeys.join(" "),
                },
              ]}
              display={keyDisplayMap}
              layout={virtualKeyboard.main}
              disableButtonHold={true}
              enableLayoutCandidates={false}
              preventMouseDownDefault={true}
              preventMouseUpDefault={true}
              stopMouseDownPropagation={true}
              stopMouseUpPropagation={true}
            />

            <div className="controlArrows">
              <Keyboard
                baseClass="simple-keyboard-control"
                theme="simple-keyboard hg-theme-default hg-layout-default"
                layoutName="default"
                onKeyPress={onKeyDown}
                onKeyReleased={onKeyUp}
                display={keyDisplayMap}
                layout={virtualKeyboard.control}
                disableButtonHold={true}
                enableLayoutCandidates={false}
                preventMouseDownDefault={true}
                preventMouseUpDefault={true}
                stopMouseDownPropagation={true}
                stopMouseUpPropagation={true}
              />
              <Keyboard
                baseClass="simple-keyboard-arrows"
                theme="simple-keyboard hg-theme-default hg-layout-default"
                onKeyPress={onKeyDown}
                onKeyReleased={onKeyUp}
                display={keyDisplayMap}
                layout={virtualKeyboard.arrows}
                disableButtonHold={true}
                enableLayoutCandidates={false}
                preventMouseDownDefault={true}
                preventMouseUpDefault={true}
                stopMouseDownPropagation={true}
                stopMouseUpPropagation={true}
              />
            </div>
            {/* TODO add optional number pad */}
          </div>
        </div>
      </Card>
    );
  };

  if (keyboardWindow && isVirtualKeyboardEnabled) {
    return createPortal(renderCard(true), keyboardWindow.container);
  }

  return (
    <div
      className="transition-all duration-500 ease-in-out"
      style={{
        marginBottom: isVirtualKeyboardEnabled ? "0px" : `-${350}px`,
      }}
    >
      <AnimatePresence>
        {isVirtualKeyboardEnabled && (
          <motion.div
            initial={{ opacity: 0, y: "100%" }}
            animate={{ opacity: 1, y: "0%" }}
            exit={{ opacity: 0, y: "100%" }}
            transition={{
              duration: 0.5,
              ease: "easeInOut",
            }}
          >
            <div
              className={cx(
                !isAttachedVirtualKeyboardVisible
                  ? "fixed top-0 left-0 z-40 overflow-hidden rounded-md shadow-2xl select-none"
                  : "relative",
              )}
              ref={keyboardRef}
              style={
                !isAttachedVirtualKeyboardVisible
                  ? {
                      transform: `translate(${placement?.x ?? 0}px, ${placement?.y ?? 0}px)`,
                      width: placement?.width ?? DEFAULT_DETACHED_WIDTH,
                      minWidth: MIN_DETACHED_WIDTH,
                      maxWidth: "100vw",
                      resize: "horizontal",
                      visibility: placement ? "visible" : "hidden",
                    }
                  : undefined
              }
            >
              {renderCard(false)}
            </div>
          </motion.div>
        )}
      </AnimatePresence>
    </div>
  );
}

export default KeyboardWrapper;
