import { create } from "zustand";

import { useRTCStore } from "@hooks/stores";
import type { FolderImage } from "@/utils/folderImage";

// Serves a folder image to the KVM over the data channel "vmedia-disk" (see
// virtual_media_browser.go). It lives outside React: the mount dialog
// closes after mounting, and the folder must stay served while this tab is
// open. After a reconnect the channel is opened again on the new peer
// connection, with the same image id.

const CHANNEL = "vmedia-disk";
const OPEN_TIMEOUT_MS = 10_000;
// The KVM asks for at most 32 KiB at a time; the length is not trusted beyond this.
const MAX_READ_BYTES = 1 << 20;

interface FolderMountState {
  image: FolderImage | null;
  name: string | null;
  connected: boolean;
  reads: number;
  bytes: number;
  errors: number;
}

export const useFolderMountStore = create<FolderMountState>(() => ({
  image: null,
  name: null,
  connected: false,
  reads: 0,
  bytes: 0,
  errors: 0,
}));

let channel: RTCDataChannel | null = null;
let channelPc: RTCPeerConnection | null = null;
let counts = { reads: 0, bytes: 0, errors: 0 };
let flushTimer: number | null = null;

function flushCounts() {
  if (counts.reads === 0 && counts.errors === 0) return;
  const s = useFolderMountStore.getState();
  useFolderMountStore.setState({
    reads: s.reads + counts.reads,
    bytes: s.bytes + counts.bytes,
    errors: s.errors + counts.errors,
  });
  counts = { reads: 0, bytes: 0, errors: 0 };
}

async function answer(ch: RTCDataChannel, image: FolderImage, request: ArrayBuffer) {
  if (request.byteLength < 16) return;
  const v = new DataView(request);
  const id = v.getUint32(0, true);
  const length = v.getUint32(4, true);
  const offset = Number(v.getBigUint64(8, true));
  let data: Uint8Array | null = null;
  try {
    if (length > MAX_READ_BYTES) throw new RangeError(`read of ${length} bytes refused`);
    data = await image.read(offset, length);
    counts.reads++;
    counts.bytes += length;
  } catch (error) {
    console.warn("folder read failed", offset, length, error);
    counts.errors++;
  }
  const reply = new Uint8Array(5 + (data?.length ?? 0));
  new DataView(reply.buffer).setUint32(0, id, true);
  reply[4] = data ? 0 : 1;
  if (data) reply.set(data, 5);
  if (ch.readyState === "open") ch.send(reply);
}

function openChannel(pc: RTCPeerConnection, image: FolderImage): Promise<void> {
  return new Promise((resolve, reject) => {
    const ch = pc.createDataChannel(CHANNEL, { ordered: true });
    ch.binaryType = "arraybuffer";
    channel = ch;
    channelPc = pc;
    const timer = window.setTimeout(() => {
      ch.close();
      reject(new Error("The folder channel to the KVM did not open."));
    }, OPEN_TIMEOUT_MS);
    ch.onopen = () => {
      window.clearTimeout(timer);
      ch.send(JSON.stringify({ id: image.id }));
      useFolderMountStore.setState({ connected: true });
      resolve();
    };
    ch.onmessage = event => {
      if (typeof event.data === "string") {
        // The KVM unmounted the folder, or it is not the mounted one.
        if (event.data === "unmounted") releaseFolder();
        return;
      }
      void answer(ch, image, event.data as ArrayBuffer);
    };
    ch.onclose = () => {
      window.clearTimeout(timer);
      if (channel === ch) {
        channel = null;
        useFolderMountStore.setState({ connected: false });
      }
    };
  });
}

// Starts serving `image` on the current connection. The caller mounts it
// with the RPC mountWithBrowser once this resolves.
export async function startFolderServing(image: FolderImage, name: string) {
  const pc = useRTCStore.getState().peerConnection;
  if (!pc) throw new Error("Not connected to the KVM.");
  releaseFolder();
  useFolderMountStore.setState({ image, name, connected: false, reads: 0, bytes: 0, errors: 0 });
  flushTimer = window.setInterval(flushCounts, 500);
  try {
    await openChannel(pc, image);
  } catch (error) {
    releaseFolder();
    throw error;
  }
}

// Stops serving and lets go of the files.
export function releaseFolder() {
  const ch = channel;
  channel = null;
  channelPc = null;
  if (ch && ch.readyState !== "closed") ch.close();
  if (flushTimer !== null) {
    window.clearInterval(flushTimer);
    flushTimer = null;
  }
  counts = { reads: 0, bytes: 0, errors: 0 };
  useFolderMountStore.setState({ image: null, name: null, connected: false });
}

// Reopen the channel when this tab reconnects with a new peer connection.
useRTCStore.subscribe(state => {
  const image = useFolderMountStore.getState().image;
  const pc = state.peerConnection;
  if (!image || !pc || pc === channelPc || state.peerConnectionState !== "connected") return;
  openChannel(pc, image).catch(error => console.warn("folder channel did not reopen", error));
});
