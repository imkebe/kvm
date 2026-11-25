import { create } from "zustand";

// What the guest terminal's shell is doing, read from its output the way
// terminals on macOS and Linux do, for the dock button shown while the panel
// is minimized:
// - OSC 133 shell integration marks (fish 4 sends them by default; bash and
//   zsh with an integration script): a command runs from C until D, and D
//   carries its exit code.
// - OSC 9;4 progress (systemd, winget and others; Ghostty, Windows Terminal
//   and Ptyxis show it).
// - The window title the shell sets, often the running command.
// - Output arriving, and the bell.
// Nothing here asks the target; a shell that sends none of the marks still
// shows output activity.

export type TerminalProgress =
  | { state: "none" }
  | { state: "normal" | "error" | "paused"; value: number }
  | { state: "indeterminate" };

interface GuestTerminalStatus {
  title: string;
  // A command runs (OSC 133); null when the shell sends no marks.
  running: boolean | null;
  // Exit code of the last finished command, when the shell reports one.
  lastExit: number | null;
  progress: TerminalProgress;
  // When output last arrived, in ms (performance.now()), coarsened.
  lastOutput: number;
  // Output or a bell while the panel was minimized.
  unread: boolean;
  bell: boolean;

  setTitle: (title: string) => void;
  shellMark: (data: string) => void;
  progressSequence: (data: string) => void;
  output: (hidden: boolean) => void;
  ring: (hidden: boolean) => void;
  // The panel is shown: what was missed is seen.
  seen: () => void;
  reset: () => void;
}

const initial = {
  title: "",
  running: null,
  lastExit: null,
  progress: { state: "none" } as TerminalProgress,
  lastOutput: 0,
  unread: false,
  bell: false,
};

// Output can arrive many times a second; the time is stored at most this
// often, which is enough for an activity light.
const OUTPUT_GRAIN_MS = 250;

export const useGuestTerminalStatus = create<GuestTerminalStatus>((set, get) => ({
  ...initial,

  setTitle: title => set({ title }),

  // OSC 133 ; A (prompt) | B (input) | C (command runs) | D [; exit code]
  shellMark: data => {
    const [mark, code] = data.split(";");
    if (mark === "C") set({ running: true });
    else if (mark === "D") {
      const exit = code === undefined || code === "" ? null : Number.parseInt(code, 10);
      set({ running: false, lastExit: Number.isNaN(exit) ? null : exit });
    } else if (mark === "A") set({ running: false });
  },

  // OSC 9 ; 4 ; state ; value. Other OSC 9 sequences are notifications and
  // are left alone.
  progressSequence: data => {
    const [kind, state, value] = data.split(";");
    if (kind !== "4") return;
    const percent = Math.min(100, Math.max(0, Number.parseInt(value ?? "0", 10) || 0));
    switch (state) {
      case "1":
        set({ progress: { state: "normal", value: percent } });
        break;
      case "2":
        set({ progress: { state: "error", value: percent } });
        break;
      case "3":
        set({ progress: { state: "indeterminate" } });
        break;
      case "4":
        set({ progress: { state: "paused", value: percent } });
        break;
      default:
        set({ progress: { state: "none" } });
    }
  },

  output: hidden => {
    const now = performance.now();
    const state = get();
    if (now - state.lastOutput < OUTPUT_GRAIN_MS && (state.unread || !hidden)) return;
    set({ lastOutput: now, unread: state.unread || hidden });
  },

  ring: hidden => {
    if (hidden) set({ bell: true });
  },

  seen: () => set({ unread: false, bell: false }),

  reset: () => set({ ...initial }),
}));
