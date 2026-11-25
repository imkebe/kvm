import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useRef,
  useState,
  type PropsWithChildren,
} from "react";
import { useRTCStore } from "@hooks/stores";
import { useDeviceRpc } from "@hooks/useDeviceRpc";

// Sound from this browser's microphone, played to the target computer as a
// USB microphone. The page captures the microphone, resamples it to 48 kHz
// mono, and sends 10 ms messages over the "microphone" data channel: one
// byte with the channel count (1), then 480 signed 16-bit little-endian
// samples. The KVM plays them into the USB audio gadget.

export interface VirtualMicrophoneState {
  enabled: boolean;
  streaming: boolean;
  deviceReady: boolean;
  sampleRate: number;
  channels: number;
  framesWritten: number;
  framesDropped: number;
  lastError?: string;
}

const TARGET_RATE = 48000;
const FRAME_SAMPLES = 480;
const MAX_BUFFERED_BYTES = 64 * 1024;

// The worklet runs on the audio thread: it resamples to 48 kHz by linear
// interpolation and cuts the stream into FRAME_SAMPLES-sample messages.
const workletSource = `
class JetKvmMic extends AudioWorkletProcessor {
  constructor() {
    super();
    this.ratio = sampleRate / ${TARGET_RATE};
    this.pos = 0;
    this.last = 0;
    this.buf = new Int16Array(${FRAME_SAMPLES});
    this.n = 0;
    this.peak = 0;
  }
  push(value) {
    const s = Math.max(-1, Math.min(1, value));
    const a = Math.abs(s);
    if (a > this.peak) this.peak = a;
    this.buf[this.n++] = s < 0 ? s * 0x8000 : s * 0x7fff;
    if (this.n === ${FRAME_SAMPLES}) {
      this.port.postMessage({ pcm: this.buf.buffer, peak: this.peak }, [this.buf.buffer]);
      this.buf = new Int16Array(${FRAME_SAMPLES});
      this.n = 0;
      this.peak = 0;
    }
  }
  process(inputs) {
    const channel = inputs[0] && inputs[0][0];
    if (!channel) return true;
    for (let i = 0; i < channel.length; i++) {
      const x = channel[i];
      while (this.pos < 1) {
        this.push(this.last + (x - this.last) * this.pos);
        this.pos += this.ratio;
      }
      this.pos -= 1;
      this.last = x;
    }
    return true;
  }
}
registerProcessor("jetkvm-mic", JetKvmMic);
`;

// openMicrophone asks the browser for this computer's microphone. Some
// browsers refuse the processing constraints of a microphone that has no
// such settings, so a second try asks for any microphone. The browser's own
// messages ("The object can not be found here") do not say what to do.
async function openMicrophone(): Promise<MediaStream> {
  const attempts: MediaStreamConstraints[] = [
    {
      audio: {
        channelCount: 1,
        echoCancellation: false,
        noiseSuppression: false,
        autoGainControl: false,
      },
      video: false,
    },
    { audio: true, video: false },
  ];
  let failure: unknown;
  for (const constraints of attempts) {
    try {
      return await navigator.mediaDevices.getUserMedia(constraints);
    } catch (error) {
      failure = error;
      const name = error instanceof DOMException ? error.name : "";
      if (name !== "NotFoundError" && name !== "OverconstrainedError") break;
    }
  }
  const name = failure instanceof DOMException ? failure.name : "";
  if (name === "NotFoundError" || name === "OverconstrainedError") {
    throw new Error(
      "This browser found no microphone. Connect a microphone, and check that the system's privacy settings allow this browser to use it.",
    );
  }
  if (name === "NotAllowedError" || name === "SecurityError") {
    throw new Error(
      "The browser blocked the microphone. Allow microphone access for this page and try again.",
    );
  }
  if (name === "NotReadableError" || name === "AbortError") {
    throw new Error("The microphone is in use by another program, or the system refused it.");
  }
  throw failure instanceof Error ? failure : new Error("Could not start the microphone");
}

function useMicrophoneController() {
  const { peerConnection } = useRTCStore();
  const { request } = useDeviceRpc();
  const [running, setRunning] = useState(false);
  const [busy, setBusy] = useState(false);
  const [muted, setMutedState] = useState(false);
  const [level, setLevel] = useState(0);
  const [error, setError] = useState("");
  const [state, setState] = useState<VirtualMicrophoneState | null>(null);
  const cleanupRef = useRef<(() => void) | null>(null);
  const mutedRef = useRef(false);
  const levelRef = useRef(0);
  const startingRef = useRef(false);

  const syncState = useCallback(async () => {
    try {
      setState(await request<VirtualMicrophoneState>("getVirtualAudioState"));
    } catch {
      /* The device may be reconnecting; the next poll retries. */
    }
  }, [request]);

  const stop = useCallback(() => {
    cleanupRef.current?.();
    cleanupRef.current = null;
    levelRef.current = 0;
    setLevel(0);
    setRunning(false);
  }, []);

  const setMuted = useCallback((next: boolean) => {
    mutedRef.current = next;
    setMutedState(next);
  }, []);

  const start = useCallback(async () => {
    if (startingRef.current || cleanupRef.current) return;
    startingRef.current = true;
    setBusy(true);
    setError("");
    const teardown: Array<() => void> = [];
    try {
      if (!navigator.mediaDevices?.getUserMedia) {
        throw new Error(
          "Microphone access requires HTTPS. Open this device over HTTPS and accept its certificate first.",
        );
      }
      if (!peerConnection) throw new Error("Peer connection is not ready");

      // Open the microphone before the USB device changes: a browser with
      // no microphone must not make the target's USB devices reconnect.
      const stream = await openMicrophone();
      teardown.push(() => stream.getTracks().forEach(track => track.stop()));

      const devices = await request<{ microphone?: boolean }>("getUsbDevices");
      if (!devices.microphone)
        await request("setUsbDeviceState", { device: "microphone", enabled: true });

      const context = new AudioContext({ latencyHint: "interactive" });
      teardown.push(() => void context.close().catch(() => {}));
      const moduleUrl = URL.createObjectURL(
        new Blob([workletSource], { type: "application/javascript" }),
      );
      try {
        await context.audioWorklet.addModule(moduleUrl);
      } finally {
        URL.revokeObjectURL(moduleUrl);
      }

      const channel = peerConnection.createDataChannel("microphone", {
        ordered: false,
        maxRetransmits: 0,
      });
      channel.binaryType = "arraybuffer";
      teardown.push(() => channel.close());
      await new Promise<void>((resolve, reject) => {
        const timer = window.setTimeout(
          () => reject(new Error("Microphone data channel timed out")),
          10000,
        );
        channel.onopen = () => {
          window.clearTimeout(timer);
          resolve();
        };
        channel.onerror = () => {
          window.clearTimeout(timer);
          reject(new Error("Microphone data channel failed"));
        };
      });

      const source = context.createMediaStreamSource(stream);
      const node = new AudioWorkletNode(context, "jetkvm-mic", {
        numberOfInputs: 1,
        numberOfOutputs: 1,
        outputChannelCount: [1],
      });
      // Some browsers only run a worklet that reaches the destination. A
      // zero gain keeps the microphone out of this computer's speakers.
      const silence = context.createGain();
      silence.gain.value = 0;
      source.connect(node);
      node.connect(silence);
      silence.connect(context.destination);
      teardown.push(() => {
        node.port.onmessage = null;
        source.disconnect();
        node.disconnect();
        silence.disconnect();
      });

      node.port.onmessage = (event: MessageEvent<{ pcm: ArrayBuffer; peak: number }>) => {
        levelRef.current = mutedRef.current ? 0 : event.data.peak;
        if (channel.readyState !== "open" || channel.bufferedAmount > MAX_BUFFERED_BYTES) return;
        const message = new Uint8Array(1 + event.data.pcm.byteLength);
        message[0] = 1;
        // A muted microphone sends silence so the target keeps a steady stream.
        if (!mutedRef.current) message.set(new Uint8Array(event.data.pcm), 1);
        channel.send(message);
      };
      channel.onclose = () => stop();
      stream.getAudioTracks()[0]?.addEventListener("ended", () => stop());

      const meter = window.setInterval(() => setLevel(levelRef.current), 100);
      teardown.push(() => window.clearInterval(meter));

      cleanupRef.current = () => teardown.reverse().forEach(fn => fn());
      setRunning(true);
      void syncState();
    } catch (failure) {
      teardown.reverse().forEach(fn => fn());
      setError(failure instanceof Error ? failure.message : "Could not start the microphone");
    } finally {
      startingRef.current = false;
      setBusy(false);
    }
  }, [peerConnection, request, stop, syncState]);

  useEffect(() => () => cleanupRef.current?.(), []);

  return { state, running, busy, muted, level, error, start, stop, setMuted, syncState };
}

const MicrophoneContext = createContext<ReturnType<typeof useMicrophoneController> | null>(null);

export function VirtualMicrophoneProvider({ children }: PropsWithChildren) {
  const controller = useMicrophoneController();
  return <MicrophoneContext.Provider value={controller}>{children}</MicrophoneContext.Provider>;
}

export function useVirtualMicrophone() {
  const context = useContext(MicrophoneContext);
  if (!context) throw new Error("Virtual microphone controls require the device provider");
  return context;
}
