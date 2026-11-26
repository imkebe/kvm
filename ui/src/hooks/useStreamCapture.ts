import { create } from "zustand";

import notifications from "@/notifications";
import { formatters } from "@/utils";
import { m } from "@localizations/messages.js";

// Screenshots and recordings of the KVM's video stream, made in the browser.
// A recording is written chunk by chunk to the origin private file system
// (OPFS) when the browser has it, so a long recording does not fill memory
// and survives a reload until it is downloaded or deleted; otherwise it is
// kept in memory until it stops.

const RECORDINGS_DIR = "jetkvm-recordings";
// Without OPFS a recording sits in memory; it stops here.
const MEMORY_LIMIT_BYTES = 500 * (1 << 20);

// WebM first (every recording browser has it), MP4 where WebM is missing
// (Safari). A type that names codecs must name one for each kind of track:
// "video/webm;codecs=vp8" refuses a stream with audio, so a stream with
// sound uses the video+audio types and a silent one the video types. The
// bare container types let the browser pick the codecs.
const VIDEO_AUDIO_TYPES = [
  "video/webm;codecs=vp9,opus",
  "video/webm;codecs=vp8,opus",
  "video/webm",
  "video/mp4;codecs=avc1,mp4a.40.2",
  "video/mp4",
];
const VIDEO_TYPES = [
  "video/webm;codecs=vp9",
  "video/webm;codecs=vp8",
  "video/webm",
  "video/mp4;codecs=avc1",
  "video/mp4",
];

export interface StoredRecording {
  name: string;
  size: number;
  modified: number;
}

interface CaptureState {
  recording: boolean;
  startedAt: number;
  bytes: number;
  error: string | null;
  recordings: StoredRecording[];
  set: (partial: Partial<CaptureState>) => void;
}

export const useCaptureStore = create<CaptureState>(set => ({
  recording: false,
  startedAt: 0,
  bytes: 0,
  error: null,
  recordings: [],
  set: partial => set(partial),
}));

function timestamp() {
  const d = new Date();
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}${pad(d.getMonth() + 1)}${pad(d.getDate())}-${pad(d.getHours())}${pad(d.getMinutes())}${pad(d.getSeconds())}`;
}

function download(blob: Blob, name: string) {
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = name;
  document.body.appendChild(a);
  a.click();
  a.remove();
  window.setTimeout(() => URL.revokeObjectURL(url), 60_000);
}

export function recordingType(withAudio = false): string | null {
  if (typeof MediaRecorder === "undefined") return null;
  const types = withAudio ? VIDEO_AUDIO_TYPES : VIDEO_TYPES;
  return types.find(type => MediaRecorder.isTypeSupported(type)) ?? null;
}

// takeScreenshot saves the video element's current frame as a PNG at the
// stream's own resolution.
export async function takeScreenshot(video: HTMLVideoElement | null) {
  if (!video || !video.videoWidth || !video.videoHeight) {
    throw new Error("There is no video to capture yet");
  }
  const canvas = document.createElement("canvas");
  canvas.width = video.videoWidth;
  canvas.height = video.videoHeight;
  const ctx = canvas.getContext("2d");
  if (!ctx) throw new Error("The browser cannot draw the frame");
  ctx.drawImage(video, 0, 0, canvas.width, canvas.height);
  const blob = await new Promise<Blob | null>(resolve => canvas.toBlob(resolve, "image/png"));
  if (!blob) throw new Error("The browser could not encode the screenshot");
  download(blob, `jetkvm-${timestamp()}.png`);
}

async function recordingsDir(): Promise<FileSystemDirectoryHandle | null> {
  try {
    const root = await navigator.storage?.getDirectory?.();
    if (!root) return null;
    return await root.getDirectoryHandle(RECORDINGS_DIR, { create: true });
  } catch {
    return null;
  }
}

export async function refreshRecordings() {
  const dir = await recordingsDir();
  if (!dir) return;
  const list: StoredRecording[] = [];
  // entries() is in every browser with OPFS; the DOM typings may lack it.
  const entries = (
    dir as unknown as { entries(): AsyncIterable<[string, FileSystemHandle]> }
  ).entries();
  for await (const [name, handle] of entries) {
    if (handle.kind !== "file") continue;
    const file = await (handle as FileSystemFileHandle).getFile();
    list.push({ name, size: file.size, modified: file.lastModified });
  }
  list.sort((a, b) => b.modified - a.modified);
  useCaptureStore.getState().set({ recordings: list });
}

export async function downloadRecording(name: string) {
  const dir = await recordingsDir();
  if (!dir) return;
  const file = await (await dir.getFileHandle(name)).getFile();
  download(file, name);
}

export async function deleteRecording(name: string) {
  const dir = await recordingsDir();
  if (!dir) return;
  await dir.removeEntry(name);
  await refreshRecordings();
}

let recorder: MediaRecorder | null = null;
let finish: (() => Promise<void>) | null = null;

export async function startRecording(stream: MediaStream | null) {
  const store = useCaptureStore.getState();
  if (store.recording) return;
  if (typeof MediaRecorder === "undefined") throw new Error("This browser cannot record video");
  const video = stream?.getVideoTracks()[0];
  if (!video) throw new Error("There is no video to record yet");
  // The target's sound goes in when there is a live audio track and the
  // browser can record it with the video; otherwise the recording is silent.
  const audio = (stream?.getAudioTracks() ?? []).filter(track => track.readyState === "live");

  const make = (tracks: MediaStreamTrack[], mimeType: string | null) => {
    const r = new MediaRecorder(new MediaStream(tracks), {
      videoBitsPerSecond: 6_000_000,
      ...(mimeType ? { mimeType } : {}),
    });
    r.start(1000);
    return r;
  };
  // Try the best type for these tracks, then the browser's own choice, then
  // the picture alone.
  const attempts: [MediaStreamTrack[], string | null][] = [];
  if (audio.length) {
    const withAudio = recordingType(true);
    if (withAudio) attempts.push([[video, ...audio], withAudio]);
    attempts.push([[video, ...audio], null]);
  }
  attempts.push([[video], recordingType(false)], [[video], null]);
  let rec: MediaRecorder | null = null;
  let lastError: unknown = null;
  for (const [tracks, type] of attempts) {
    try {
      rec = make(tracks, type);
      break;
    } catch (error) {
      lastError = error;
    }
  }
  if (!rec)
    throw lastError instanceof Error ? lastError : new Error("The browser refused to record");

  const type = rec.mimeType || "video/webm";
  const extension = type.startsWith("video/mp4") ? "mp4" : "webm";
  const name = `jetkvm-${timestamp()}.${extension}`;

  // Chunks go to an OPFS file when the browser can write one; until the
  // file is open they wait in the write chain.
  let writable: FileSystemWritableFileStream | null = null;
  const memory: Blob[] = [];
  let memoryBytes = 0;
  let bytes = 0;
  let writes: Promise<void> = (async () => {
    const dir = await recordingsDir();
    if (!dir) return;
    try {
      const handle = await dir.getFileHandle(name, { create: true });
      writable = await handle.createWritable();
    } catch {
      writable = null;
    }
  })();

  rec.ondataavailable = event => {
    if (!event.data.size) return;
    bytes += event.data.size;
    useCaptureStore.getState().set({ bytes });
    const chunk = event.data;
    writes = writes
      .then(async () => {
        if (writable) await writable.write(chunk);
        else {
          memory.push(chunk);
          memoryBytes += chunk.size;
          if (memoryBytes > MEMORY_LIMIT_BYTES) {
            notifications.error(
              m.capture_memory_limit({ limit: formatters.bytes(MEMORY_LIMIT_BYTES, 0) }),
            );
            void stopRecording();
          }
        }
      })
      .catch(error => {
        useCaptureStore.getState().set({ error: String(error) });
      });
  };
  const current = rec;
  const stopped = new Promise<void>(resolve => (current.onstop = () => resolve()));
  finish = async () => {
    await stopped;
    await writes;
    const w = writable as FileSystemWritableFileStream | null;
    if (w) {
      await w.close();
      await downloadRecording(name);
      await refreshRecordings();
    } else {
      download(new Blob(memory, { type }), name);
    }
  };
  // A track that ends (stream lost) stops the recording and keeps what came.
  video.addEventListener("ended", () => void stopRecording(), { once: true });
  recorder = rec;
  store.set({ recording: true, startedAt: Date.now(), bytes: 0, error: null });
}

export async function stopRecording() {
  const rec = recorder;
  const done = finish;
  if (!rec || !done) return;
  recorder = null;
  finish = null;
  if (rec.state !== "inactive") rec.stop();
  try {
    await done();
  } finally {
    useCaptureStore.getState().set({ recording: false });
  }
}
