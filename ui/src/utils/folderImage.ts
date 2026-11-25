// A read-only disk made from a folder the user picked, without copying it:
// the partition table, boot sectors, FATs and directories are made up front
// (FAT entries and the exFAT allocation bitmap on demand), file contents are
// read from the File objects when the target asks for them.
//
// Layout: an MBR with one partition at 1 MiB, so firmware and operating
// systems treat it like a USB stick. Every directory and file gets contiguous
// clusters. Two file systems:
//   - FAT32: every system and firmware reads it (a UEFI computer can boot from
//     it when the folder holds EFI/BOOT/BOOTX64.EFI); files up to 4 GiB - 1.
//   - exFAT: Windows, macOS and Linux 5.7+ read it; no file size limit, one
//     bit per cluster of allocation bitmap, no 8.3 names.

import { EXFAT_UPCASE_BASE64 } from "./exfatUpcase";

export type FolderFs = "fat32" | "exfat";

export interface FolderFile {
  // Path inside the folder, with "/" between parts.
  path: string;
  file: Pick<File, "size" | "lastModified" | "slice">;
}

export interface FolderImage {
  id: string;
  fs: FolderFs;
  label: string;
  size: number;
  files: number;
  dirs: number;
  dataBytes: number;
  // Files the file system cannot hold (FAT32: 4 GiB and larger).
  skipped: string[];
  // Names changed to be valid on the file system.
  renamed: number;
  read(offset: number, length: number): Promise<Uint8Array>;
}

// The largest file FAT32 holds.
export const FAT32_MAX_FILE = 0xffffffff;

const SECTOR = 512;
const PART_START = 2048; // sectors

interface FileNode {
  name: string;
  file: FolderFile["file"];
  cluster: number;
  clusters: number;
}

interface DirNode {
  name: string;
  parent: DirNode | null;
  dirs: DirNode[];
  files: FileNode[];
  names: Set<string>; // folded names in use
  cluster: number;
  clusters: number;
  slots: number; // 32-byte directory entries
  mtime: number;
  buffer?: Uint8Array;
}

interface Extent {
  start: number; // first cluster
  end: number; // one past the last cluster
  dir?: DirNode;
  file?: FileNode;
  bytes?: Uint8Array;
  // Bytes [from, to) of the extent, made on demand.
  gen?: (from: number, to: number) => Uint8Array;
}

const INVALID_LONG = new Set('"*/:<>?\\|');

// Reserved and control characters become "_".
function replaceInvalid(name: string) {
  let out = "";
  for (const ch of name) {
    const code = ch.charCodeAt(0);
    out += code < 0x20 || code === 0x7f || INVALID_LONG.has(ch) ? "_" : ch;
  }
  return out;
}

function randomId() {
  const bytes = new Uint8Array(8);
  crypto.getRandomValues(bytes);
  return Array.from(bytes, b => b.toString(16).padStart(2, "0")).join("");
}

// A name valid on FAT and exFAT: no reserved characters, no trailing dots or
// spaces, at most 255 UTF-16 units.
function longName(name: string) {
  let out = replaceInvalid(name.normalize("NFC")).replace(/[. ]+$/, "");
  if (out.length > 255) {
    const dot = out.lastIndexOf(".");
    const ext = dot > 0 && out.length - dot <= 16 ? out.slice(dot) : "";
    out = out.slice(0, 255 - ext.length) + ext;
  }
  return out || "_";
}

// The name made unique in its directory, "name (2).ext" style. Both file
// systems compare names without case; `fold` is that comparison.
function uniqueName(dir: DirNode, name: string, fold: (s: string) => string) {
  if (!dir.names.has(fold(name))) return name;
  const dot = name.lastIndexOf(".");
  const base = dot > 0 ? name.slice(0, dot) : name;
  const ext = dot > 0 ? name.slice(dot) : "";
  for (let n = 2; ; n++) {
    const candidate = `${base} (${n})${ext}`;
    if (!dir.names.has(fold(candidate))) return candidate;
  }
}

interface Tree {
  root: DirNode;
  dirs: DirNode[]; // breadth-first, root first
  fileCount: number;
  dataBytes: number;
  skipped: string[];
  renamed: number;
}

function buildTree(entries: FolderFile[], maxFile: number, fold: (s: string) => string): Tree {
  const now = Date.now();
  const newDir = (name: string, parent: DirNode | null): DirNode => ({
    name,
    parent,
    dirs: [],
    files: [],
    names: new Set(),
    cluster: 0,
    clusters: 0,
    slots: 0,
    mtime: now,
  });
  const root = newDir("", null);
  const skipped: string[] = [];
  let renamed = 0;
  let dataBytes = 0;
  let fileCount = 0;
  const dirIndex = new Map<string, DirNode>([["", root]]);

  for (const entry of entries) {
    const parts = entry.path.split("/").filter(p => p !== "");
    if (parts.length === 0) continue;
    if (entry.file.size > maxFile) {
      skipped.push(entry.path);
      continue;
    }
    let dir = root;
    let key = "";
    for (const part of parts.slice(0, -1)) {
      key += "/" + part;
      let next = dirIndex.get(key);
      if (!next) {
        const name = uniqueName(dir, longName(part), fold);
        if (name !== part) renamed++;
        next = newDir(name, dir);
        dir.names.add(fold(name));
        dir.dirs.push(next);
        dirIndex.set(key, next);
      }
      dir = next;
    }
    const original = parts[parts.length - 1];
    const name = uniqueName(dir, longName(original), fold);
    if (name !== original) renamed++;
    dir.names.add(fold(name));
    dir.files.push({ name, file: entry.file, cluster: 0, clusters: 0 });
    dir.mtime = Math.max(dir.mtime === now ? 0 : dir.mtime, entry.file.lastModified) || now;
    dataBytes += entry.file.size;
    fileCount++;
  }

  const dirs: DirNode[] = [];
  for (let i = 0, queue = [root]; i < queue.length; i++) {
    dirs.push(queue[i]);
    queue.push(...queue[i].dirs);
  }
  return { root, dirs, fileCount, dataBytes, skipped, renamed };
}

// Gives directories (in order), then files, contiguous clusters from `next`;
// returns the first free cluster.
function allocate(tree: Tree, next: number, clusterSize: number, extents: Extent[]) {
  for (const dir of tree.dirs) {
    dir.clusters = Math.max(1, Math.ceil((dir.slots * 32) / clusterSize));
    dir.cluster = next;
    extents.push({ start: next, end: next + dir.clusters, dir });
    next += dir.clusters;
  }
  for (const dir of tree.dirs) {
    for (const file of dir.files) {
      if (file.file.size === 0) continue;
      file.clusters = Math.ceil(file.file.size / clusterSize);
      file.cluster = next;
      extents.push({ start: next, end: next + file.clusters, file });
      next += file.clusters;
    }
  }
  return next;
}

function mbrSector(diskId: number, type: number, sectors: number) {
  const mbr = new Uint8Array(SECTOR);
  const v = new DataView(mbr.buffer);
  mbr.set([0xfa, 0xf4, 0xeb, 0xfd], 0); // cli; hlt; jmp $-1
  v.setUint32(440, diskId, true);
  const p = 446;
  v.setUint8(p, 0x80);
  mbr.set([0xfe, 0xff, 0xff], p + 1);
  v.setUint8(p + 4, type);
  mbr.set([0xfe, 0xff, 0xff], p + 5);
  v.setUint32(p + 8, PART_START, true);
  v.setUint32(p + 12, sectors, true);
  v.setUint16(510, 0xaa55, true);
  return mbr;
}

interface Layout {
  size: number;
  fixed: { at: number; bytes: Uint8Array }[];
  fats: { start: number; bytes: number }[];
  fatEntry: (i: number) => number;
  dataStart: number;
  dataEnd: number;
  clusterSize: number;
  extents: Extent[];
}

// The extent holding a cluster, by binary search.
function extentIndex(extents: Extent[], cluster: number) {
  let lo = 0;
  let hi = extents.length - 1;
  while (lo <= hi) {
    const mid = (lo + hi) >> 1;
    const e = extents[mid];
    if (cluster < e.start) hi = mid - 1;
    else if (cluster >= e.end) lo = mid + 1;
    else return mid;
  }
  return -1;
}

// FAT entries of contiguous chains: the next cluster, or `eoc` at the end.
function chainEntry(extents: Extent[], i: number, eoc: number) {
  const at = extentIndex(extents, i);
  if (at < 0) return 0;
  return i === extents[at].end - 1 ? eoc : i + 1;
}

function makeReader(l: Layout) {
  return async (offset: number, length: number): Promise<Uint8Array> => {
    if (!Number.isSafeInteger(offset) || offset < 0 || length < 0 || offset + length > l.size) {
      throw new RangeError(`read ${offset}+${length} outside the image`);
    }
    const out = new Uint8Array(length);
    const end = offset + length;

    // Copies the part of [start, start + bytes.length) that falls in the read.
    const put = (start: number, bytes: Uint8Array) => {
      const a = Math.max(offset, start);
      const b = Math.min(end, start + bytes.length);
      if (a < b) out.set(bytes.subarray(a - start, b - start), a - offset);
    };

    for (const part of l.fixed) put(part.at, part.bytes);

    for (const fat of l.fats) {
      const a = Math.max(offset, fat.start);
      const b = Math.min(end, fat.start + fat.bytes);
      if (a >= b) continue;
      const first = Math.floor((a - fat.start) / 4);
      const last = Math.ceil((b - fat.start) / 4);
      const entries = new Uint8Array((last - first) * 4);
      const v = new DataView(entries.buffer);
      for (let i = first; i < last; i++) v.setUint32((i - first) * 4, l.fatEntry(i), true);
      put(fat.start + first * 4, entries);
    }

    const a = Math.max(offset, l.dataStart);
    const b = Math.min(end, l.dataEnd);
    if (a < b) {
      const reads: Promise<void>[] = [];
      const cluster = 2 + Math.floor((a - l.dataStart) / l.clusterSize);
      let at = extentIndex(l.extents, cluster);
      if (at < 0) {
        // In free space: find the first extent after it.
        at = l.extents.findIndex(e => e.start > cluster);
        if (at < 0) at = l.extents.length;
      }
      for (; at < l.extents.length; at++) {
        const e = l.extents[at];
        const start = l.dataStart + (e.start - 2) * l.clusterSize;
        if (start >= b) break;
        const bytes = e.dir?.buffer ?? e.bytes;
        if (bytes) put(start, bytes);
        if (e.gen) {
          const from = Math.max(a, start) - start;
          const to = Math.min(b, start + (e.end - e.start) * l.clusterSize) - start;
          if (from < to) put(start + from, e.gen(from, to));
        }
        if (e.file) {
          const f = e.file.file;
          const from = Math.max(a, start) - start;
          const to = Math.min(b, start + f.size) - start;
          if (from < to) {
            reads.push(
              f
                .slice(from, to)
                .arrayBuffer()
                .then(buf => put(start + from, new Uint8Array(buf))),
            );
          }
        }
      }
      await Promise.all(reads);
    }
    return out;
  };
}

// ---------------------------------------------------------------------------
// FAT32

const FAT32_RESERVED = 32; // sectors
const FAT32_COPIES = 2;
const FAT32_MIN_CLUSTERS = 66000; // FAT32 needs at least 65525
const FAT32_MAX_DIR_ENTRIES = 65536;
const FAT32_EOC = 0x0fffffff;
const SHORT_OK = /^[A-Z0-9$%'\-_@~`!(){}^#&]$/;
const SHORT_PLAIN = /^[A-Z0-9$%'\-_@~`!(){}^#&]*$/;

function shortChars(s: string) {
  let out = "";
  for (const ch of s.toUpperCase()) {
    if (ch === " " || ch === ".") continue;
    out += SHORT_OK.test(ch) ? ch : "_";
  }
  return out;
}

interface ShortName {
  bytes: Uint8Array; // 11 bytes
  needsLong: boolean;
}

function shortBytes(base: string, ext: string) {
  const bytes = new Uint8Array(11).fill(0x20);
  for (let i = 0; i < base.length; i++) bytes[i] = base.charCodeAt(i);
  for (let i = 0; i < ext.length; i++) bytes[8 + i] = ext.charCodeAt(i);
  return bytes;
}

// The 8.3 name of a long name, unique within `used`.
function shortName(name: string, used: Set<string>): ShortName {
  const dot = name.lastIndexOf(".");
  const base = dot > 0 ? name.slice(0, dot) : name;
  const ext = dot > 0 ? name.slice(dot + 1) : "";
  const plain = (s: string, max: number) => s.length <= max && SHORT_PLAIN.test(s.toUpperCase());

  if (base.length > 0 && plain(base, 8) && plain(ext, 3) && (dot < 0 || ext.length > 0)) {
    const key = `${base.toUpperCase()}.${ext.toUpperCase()}`;
    if (!used.has(key)) {
      used.add(key);
      // Any lower-case letter gets a long name too: every system reads those,
      // not every one honours the NT lower-case flags.
      return {
        bytes: shortBytes(base.toUpperCase(), ext.toUpperCase()),
        needsLong: name !== name.toUpperCase(),
      };
    }
  }

  const b = shortChars(base) || "_";
  const e = shortChars(ext).slice(0, 3);
  for (let n = 1; ; n++) {
    const tail = `~${n}`;
    const candidate = b.slice(0, 8 - tail.length) + tail;
    const key = `${candidate}.${e}`;
    if (!used.has(key)) {
      used.add(key);
      return { bytes: shortBytes(candidate, e), needsLong: true };
    }
  }
}

function shortChecksum(short: Uint8Array) {
  let sum = 0;
  for (const b of short) sum = (((sum & 1) << 7) + (sum >> 1) + b) & 0xff;
  return sum;
}

function fatDateTime(ms: number) {
  const d = new Date(ms);
  const year = d.getFullYear();
  if (year < 1980) return { date: 0x21, time: 0 }; // 1980-01-01
  if (year > 2107) return { date: (127 << 9) | (12 << 5) | 31, time: 0 };
  const date = ((year - 1980) << 9) | ((d.getMonth() + 1) << 5) | d.getDate();
  const time = (d.getHours() << 11) | (d.getMinutes() << 5) | Math.floor(d.getSeconds() / 2);
  return { date, time };
}

function writeFatEntry(
  view: DataView,
  at: number,
  name: Uint8Array,
  attr: number,
  cluster: number,
  size: number,
  mtime: number,
) {
  new Uint8Array(view.buffer, view.byteOffset + at, 11).set(name);
  view.setUint8(at + 11, attr);
  const { date, time } = fatDateTime(mtime);
  view.setUint16(at + 14, time, true); // created
  view.setUint16(at + 16, date, true);
  view.setUint16(at + 18, date, true); // accessed
  view.setUint16(at + 20, cluster >>> 16, true);
  view.setUint16(at + 22, time, true); // written
  view.setUint16(at + 24, date, true);
  view.setUint16(at + 26, cluster & 0xffff, true);
  view.setUint32(at + 28, size, true);
}

const LFN_OFFSETS = [1, 3, 5, 7, 9, 14, 16, 18, 20, 22, 24, 28, 30];

function writeLongEntries(view: DataView, at: number, name: string, sum: number) {
  const count = Math.ceil(name.length / 13);
  for (let i = 0; i < count; i++) {
    const seq = count - i; // entries go last part first
    const pos = at + i * 32;
    view.setUint8(pos, seq | (seq === count ? 0x40 : 0));
    view.setUint8(pos + 11, 0x0f);
    view.setUint8(pos + 13, sum);
    for (let k = 0; k < 13; k++) {
      const idx = (seq - 1) * 13 + k;
      const unit = idx < name.length ? name.charCodeAt(idx) : idx === name.length ? 0 : 0xffff;
      view.setUint16(pos + LFN_OFFSETS[k], unit, true);
    }
  }
  return count;
}

// Cluster size by data size, as Windows formats FAT32.
function fat32ClusterSize(bytes: number) {
  const mb = bytes / (1 << 20);
  if (mb < 64) return 512;
  if (mb < 128) return 1024;
  if (mb < 256) return 2048;
  if (mb < 8192) return 4096;
  if (mb < 16384) return 8192;
  if (mb < 32768) return 16384;
  return 32768;
}

interface Child {
  long: string;
  short: ShortName;
  dir?: DirNode;
  file?: FileNode;
}

function buildFat32(folderName: string, tree: Tree, id: string) {
  const now = Date.now();
  const { root, dirs } = tree;

  // Directory entries, with 8.3 names.
  const children = new Map<DirNode, Child[]>();
  for (const dir of dirs) {
    const used = new Set<string>();
    const list: Child[] = [];
    for (const sub of dir.dirs)
      list.push({ long: sub.name, short: shortName(sub.name, used), dir: sub });
    for (const file of dir.files)
      list.push({ long: file.name, short: shortName(file.name, used), file });
    let slots = dir === root ? 1 : 2; // volume label, or "." and ".."
    for (const child of list)
      slots += 1 + (child.short.needsLong ? Math.ceil(child.long.length / 13) : 0);
    if (slots > FAT32_MAX_DIR_ENTRIES) throw new Error(`Too many entries in folder "${dir.name}"`);
    dir.slots = slots;
    children.set(dir, list);
  }

  const dirBytes = dirs.reduce((sum, d) => sum + d.slots * 32, 0);
  const clusterSize = fat32ClusterSize(tree.dataBytes + dirBytes);
  const sectorsPerCluster = clusterSize / SECTOR;

  const extents: Extent[] = [];
  const next = allocate(tree, 2, clusterSize, extents);
  const clusters = Math.max(next - 2, FAT32_MIN_CLUSTERS);
  const fatSectors = Math.ceil(((clusters + 2) * 4) / SECTOR);
  const volumeSectors = FAT32_RESERVED + FAT32_COPIES * fatSectors + clusters * sectorsPerCluster;
  const size = Math.ceil(((PART_START + volumeSectors) * SECTOR) / 4096) * 4096;

  const label = (shortChars(folderName).slice(0, 11) || "JETKVM").padEnd(11, " ");
  const labelBytes = Uint8Array.from(label, ch => ch.charCodeAt(0));

  for (const dir of dirs) {
    const buffer = new Uint8Array(dir.slots * 32);
    const view = new DataView(buffer.buffer);
    let at = 0;
    if (dir === root) {
      writeFatEntry(view, 0, labelBytes, 0x08, 0, 0, now);
      at = 32;
    } else {
      const parentCluster = dir.parent === root || !dir.parent ? 0 : dir.parent.cluster;
      writeFatEntry(view, 0, shortBytes(".", ""), 0x10, dir.cluster, 0, dir.mtime);
      writeFatEntry(view, 32, shortBytes("..", ""), 0x10, parentCluster, 0, dir.mtime);
      at = 64;
    }
    for (const child of children.get(dir) ?? []) {
      if (child.short.needsLong) {
        at += 32 * writeLongEntries(view, at, child.long, shortChecksum(child.short.bytes));
      }
      if (child.dir) {
        writeFatEntry(view, at, child.short.bytes, 0x10, child.dir.cluster, 0, child.dir.mtime);
      } else if (child.file) {
        const f = child.file;
        const mtime = f.file.lastModified || now;
        writeFatEntry(view, at, child.short.bytes, 0x20, f.cluster, f.file.size, mtime);
      }
      at += 32;
    }
    dir.buffer = buffer;
  }

  const volumeId = parseInt(id.slice(0, 8), 16) >>> 0;
  const mbr = mbrSector(volumeId, 0x0c, volumeSectors); // FAT32 with LBA

  const boot = new Uint8Array(SECTOR);
  {
    const v = new DataView(boot.buffer);
    boot.set([0xeb, 0x58, 0x90], 0);
    boot.set(
      Uint8Array.from("JETKVM  ", ch => ch.charCodeAt(0)),
      3,
    );
    v.setUint16(11, SECTOR, true);
    v.setUint8(13, sectorsPerCluster);
    v.setUint16(14, FAT32_RESERVED, true);
    v.setUint8(16, FAT32_COPIES);
    v.setUint8(21, 0xf8);
    v.setUint16(24, 63, true);
    v.setUint16(26, 255, true);
    v.setUint32(28, PART_START, true);
    v.setUint32(32, volumeSectors, true);
    v.setUint32(36, fatSectors, true);
    v.setUint32(44, 2, true); // root directory cluster
    v.setUint16(48, 1, true); // FSInfo sector
    v.setUint16(50, 6, true); // backup boot sector
    v.setUint8(64, 0x80);
    v.setUint8(66, 0x29);
    v.setUint32(67, volumeId, true);
    boot.set(labelBytes, 71);
    boot.set(
      Uint8Array.from("FAT32   ", ch => ch.charCodeAt(0)),
      82,
    );
    boot.set([0xfa, 0xf4, 0xeb, 0xfd], 90);
    v.setUint16(510, 0xaa55, true);
  }

  const fsInfo = new Uint8Array(SECTOR);
  {
    const v = new DataView(fsInfo.buffer);
    v.setUint32(0, 0x41615252, true);
    v.setUint32(484, 0x61417272, true);
    v.setUint32(488, clusters - (next - 2), true); // free clusters
    v.setUint32(492, 0xffffffff, true);
    v.setUint32(508, 0xaa550000, true);
  }

  const volStart = PART_START * SECTOR;
  const fatStart = volStart + FAT32_RESERVED * SECTOR;
  const fatBytes = fatSectors * SECTOR;
  const dataStart = fatStart + FAT32_COPIES * fatBytes;

  const read = makeReader({
    size,
    fixed: [
      { at: 0, bytes: mbr },
      { at: volStart, bytes: boot },
      { at: volStart + SECTOR, bytes: fsInfo },
      { at: volStart + 6 * SECTOR, bytes: boot },
      { at: volStart + 7 * SECTOR, bytes: fsInfo },
    ],
    fats: Array.from({ length: FAT32_COPIES }, (_, copy) => ({
      start: fatStart + copy * fatBytes,
      bytes: fatBytes,
    })),
    fatEntry: i => (i === 0 ? 0x0ffffff8 : i === 1 ? FAT32_EOC : chainEntry(extents, i, FAT32_EOC)),
    dataStart,
    dataEnd: dataStart + clusters * clusterSize,
    clusterSize,
    extents,
  });
  return { label: label.trim(), size, read };
}

// ---------------------------------------------------------------------------
// exFAT

const EXFAT_FAT_OFFSET = 128; // sectors
const EXFAT_EOC = 0xffffffff;
const EXFAT_MIN_BYTES = 4 << 20;
const EXFAT_MAX_DIR_BYTES = 256 << 20;

let upcaseTable: { bytes: Uint8Array; map: Uint16Array; checksum: number } | null = null;

function sum32(sum: number, bytes: Uint8Array, skip?: (i: number) => boolean) {
  for (let i = 0; i < bytes.length; i++) {
    if (skip?.(i)) continue;
    sum = ((sum & 1 ? 0x80000000 : 0) + (sum >>> 1) + bytes[i]) >>> 0;
  }
  return sum;
}

function sum16(sum: number, byte: number) {
  return ((sum & 1 ? 0x8000 : 0) + (sum >>> 1) + byte) & 0xffff;
}

// The up-case table, unpacked into a map for name hashes and comparisons.
function exfatUpcase() {
  if (upcaseTable) return upcaseTable;
  const bytes = Uint8Array.from(atob(EXFAT_UPCASE_BASE64), c => c.charCodeAt(0));
  const map = new Uint16Array(65536);
  for (let i = 0; i < map.length; i++) map[i] = i;
  let index = 0;
  for (let i = 0; i + 1 < bytes.length && index < map.length; i += 2) {
    const unit = bytes[i] | (bytes[i + 1] << 8);
    if (unit === 0xffff && i + 3 < bytes.length) {
      i += 2;
      index += bytes[i] | (bytes[i + 1] << 8);
    } else {
      map[index++] = unit;
    }
  }
  upcaseTable = { bytes, map, checksum: sum32(0, bytes) };
  return upcaseTable;
}

function exfatFold(name: string) {
  const { map } = exfatUpcase();
  let out = "";
  for (let i = 0; i < name.length; i++) out += String.fromCharCode(map[name.charCodeAt(i)]);
  return out;
}

function exfatNameHash(name: string) {
  const { map } = exfatUpcase();
  let hash = 0;
  for (let i = 0; i < name.length; i++) {
    const unit = map[name.charCodeAt(i)];
    hash = sum16(sum16(hash, unit & 0xff), unit >> 8);
  }
  return hash;
}

// A UTC timestamp and its 10 ms part; the entries say the offset is 0.
function exfatTime(ms: number) {
  const d = new Date(ms);
  const year = d.getUTCFullYear();
  if (year < 1980) return { ts: (1 << 21) | (1 << 16), inc: 0 };
  if (year > 2107) return { ts: ((127 << 25) | (12 << 21) | (31 << 16) | 0xbf7d) >>> 0, inc: 0 };
  const ts =
    (((year - 1980) << 25) |
      ((d.getUTCMonth() + 1) << 21) |
      (d.getUTCDate() << 16) |
      (d.getUTCHours() << 11) |
      (d.getUTCMinutes() << 5) |
      (d.getUTCSeconds() >> 1)) >>>
    0;
  const inc = (d.getUTCSeconds() & 1) * 100 + Math.floor(d.getUTCMilliseconds() / 10);
  return { ts, inc };
}

function setUint64(view: DataView, at: number, value: number) {
  view.setBigUint64(at, BigInt(value), true);
}

function exfatSetSlots(name: string) {
  return 2 + Math.ceil(name.length / 15);
}

// A file entry set: the file entry, the stream extension and the names.
function writeExfatSet(
  buffer: Uint8Array,
  at: number,
  name: string,
  attr: number,
  cluster: number,
  dataLength: number,
  validLength: number,
  mtime: number,
) {
  const view = new DataView(buffer.buffer, buffer.byteOffset);
  const slots = exfatSetSlots(name);
  const { ts, inc } = exfatTime(mtime);
  view.setUint8(at, 0x85);
  view.setUint8(at + 1, slots - 1);
  view.setUint16(at + 4, attr, true);
  view.setUint32(at + 8, ts, true); // created
  view.setUint32(at + 12, ts, true); // modified
  view.setUint32(at + 16, ts, true); // accessed
  view.setUint8(at + 20, inc);
  view.setUint8(at + 21, inc);
  view.setUint8(at + 22, 0x80); // UTC
  view.setUint8(at + 23, 0x80);
  view.setUint8(at + 24, 0x80);

  const s = at + 32;
  view.setUint8(s, 0xc0);
  view.setUint8(s + 1, 0x01); // allocation possible, clusters chained in the FAT
  view.setUint8(s + 3, name.length);
  view.setUint16(s + 4, exfatNameHash(name), true);
  setUint64(view, s + 8, validLength);
  view.setUint32(s + 20, cluster, true);
  setUint64(view, s + 24, dataLength);

  for (let k = 0; k < slots - 2; k++) {
    const e = at + 64 + k * 32;
    view.setUint8(e, 0xc1);
    for (let j = 0; j < 15; j++) {
      const idx = k * 15 + j;
      view.setUint16(e + 2 + j * 2, idx < name.length ? name.charCodeAt(idx) : 0, true);
    }
  }

  let sum = 0;
  for (let i = 0; i < slots * 32; i++) {
    if (i === 2 || i === 3) continue;
    sum = sum16(sum, buffer[at + i]);
  }
  view.setUint16(at + 2, sum, true);
}

// Cluster size by data size, as Windows formats exFAT.
function exfatClusterSize(bytes: number) {
  const mb = bytes / (1 << 20);
  if (mb < 256) return 4096;
  if (mb < 32768) return 32768;
  return 131072;
}

function buildExfat(folderName: string, tree: Tree, id: string) {
  const now = Date.now();
  const { root, dirs } = tree;
  const upcase = exfatUpcase();

  for (const dir of dirs) {
    let slots = dir === root ? 3 : 0; // volume label, allocation bitmap, up-case table
    for (const sub of dir.dirs) slots += exfatSetSlots(sub.name);
    for (const file of dir.files) slots += exfatSetSlots(file.name);
    if (slots * 32 > EXFAT_MAX_DIR_BYTES)
      throw new Error(`Too many entries in folder "${dir.name}"`);
    dir.slots = slots;
  }

  const dirBytes = dirs.reduce((sum, d) => sum + d.slots * 32, 0);
  const clusterSize = exfatClusterSize(tree.dataBytes + dirBytes);
  const sectorsPerCluster = clusterSize / SECTOR;
  const clustersOf = (bytes: number) => Math.ceil(bytes / clusterSize);

  let contentClusters = clustersOf(upcase.bytes.length);
  for (const dir of dirs) contentClusters += Math.max(1, clustersOf(dir.slots * 32));
  for (const dir of dirs) for (const f of dir.files) contentClusters += clustersOf(f.file.size);

  // The bitmap covers every cluster, itself included.
  let bitmapClusters = 1;
  let clusterCount = 0;
  for (;;) {
    clusterCount = Math.max(bitmapClusters + contentClusters, clustersOf(EXFAT_MIN_BYTES));
    const need = clustersOf(Math.ceil(clusterCount / 8));
    if (need <= bitmapClusters) break;
    bitmapClusters = need;
  }
  if (clusterCount > 0xfffffff5) throw new Error("The folder is too large for exFAT");
  const bitmapBytes = Math.ceil(clusterCount / 8);

  const extents: Extent[] = [];
  const usedBefore = 2 + bitmapClusters;
  const upcaseCluster = usedBefore;
  const upcaseClusters = clustersOf(upcase.bytes.length);
  const next = allocate(tree, upcaseCluster + upcaseClusters, clusterSize, extents);
  const used = next - 2;
  extents.unshift(
    {
      start: 2,
      end: 2 + bitmapClusters,
      gen: (from, to) => {
        const out = new Uint8Array(to - from);
        for (let k = from; k < Math.min(to, bitmapBytes); k++) {
          const bit = k * 8;
          out[k - from] = bit + 8 <= used ? 0xff : bit >= used ? 0 : (1 << (used - bit)) - 1;
        }
        return out;
      },
    },
    { start: upcaseCluster, end: upcaseCluster + upcaseClusters, bytes: upcase.bytes },
  );

  const fatSectors = Math.ceil(((clusterCount + 2) * 4) / SECTOR);
  const heapOffset =
    Math.ceil((EXFAT_FAT_OFFSET + fatSectors) / sectorsPerCluster) * sectorsPerCluster;
  const volumeSectors = heapOffset + clusterCount * sectorsPerCluster;
  const size = Math.ceil(((PART_START + volumeSectors) * SECTOR) / 4096) * 4096;

  const label = replaceInvalid(folderName.normalize("NFC")).trim().slice(0, 11) || "JETKVM";

  for (const dir of dirs) {
    const buffer = new Uint8Array(dir.slots * 32);
    const view = new DataView(buffer.buffer);
    let at = 0;
    if (dir === root) {
      view.setUint8(0, 0x83);
      view.setUint8(1, label.length);
      for (let i = 0; i < label.length; i++) view.setUint16(2 + i * 2, label.charCodeAt(i), true);
      view.setUint8(32, 0x81);
      view.setUint32(32 + 20, 2, true);
      setUint64(view, 32 + 24, bitmapBytes);
      view.setUint8(64, 0x82);
      view.setUint32(64 + 4, upcase.checksum, true);
      view.setUint32(64 + 20, upcaseCluster, true);
      setUint64(view, 64 + 24, upcase.bytes.length);
      at = 96;
    }
    for (const sub of dir.dirs) {
      const length = sub.clusters * clusterSize;
      writeExfatSet(buffer, at, sub.name, 0x10, sub.cluster, length, length, sub.mtime);
      at += exfatSetSlots(sub.name) * 32;
    }
    for (const f of dir.files) {
      const mtime = f.file.lastModified || now;
      writeExfatSet(buffer, at, f.name, 0x20, f.cluster, f.file.size, f.file.size, mtime);
      at += exfatSetSlots(f.name) * 32;
    }
    dir.buffer = buffer;
  }

  const serial = parseInt(id.slice(0, 8), 16) >>> 0;
  const mbr = mbrSector(serial, 0x07, volumeSectors);

  // Main boot region: boot sector, 8 extended boot sectors, OEM parameters,
  // a reserved sector and the checksum sector.
  const bootRegion = new Uint8Array(12 * SECTOR);
  {
    const v = new DataView(bootRegion.buffer);
    bootRegion.set([0xeb, 0x76, 0x90], 0);
    bootRegion.set(
      Uint8Array.from("EXFAT   ", ch => ch.charCodeAt(0)),
      3,
    );
    setUint64(v, 64, PART_START);
    setUint64(v, 72, volumeSectors);
    v.setUint32(80, EXFAT_FAT_OFFSET, true);
    v.setUint32(84, fatSectors, true);
    v.setUint32(88, heapOffset, true);
    v.setUint32(92, clusterCount, true);
    v.setUint32(96, root.cluster, true);
    v.setUint32(100, serial, true);
    v.setUint16(104, 0x0100, true); // revision 1.00
    v.setUint8(108, 9); // 512-byte sectors
    v.setUint8(109, Math.log2(sectorsPerCluster));
    v.setUint8(110, 1); // one FAT
    v.setUint8(111, 0x80);
    v.setUint8(112, Math.floor((used * 100) / clusterCount));
    bootRegion.set([0xf4, 0xeb, 0xfd], 120); // hlt; jmp $-1
    v.setUint16(510, 0xaa55, true);
    for (let s = 1; s <= 8; s++) v.setUint32(s * SECTOR + 508, 0xaa550000, true);
    const sum = sum32(
      0,
      bootRegion.subarray(0, 11 * SECTOR),
      i => i === 106 || i === 107 || i === 112,
    );
    for (let i = 0; i < SECTOR; i += 4) v.setUint32(11 * SECTOR + i, sum, true);
  }

  const volStart = PART_START * SECTOR;
  const fatStart = volStart + EXFAT_FAT_OFFSET * SECTOR;
  const dataStart = volStart + heapOffset * SECTOR;

  const read = makeReader({
    size,
    fixed: [
      { at: 0, bytes: mbr },
      { at: volStart, bytes: bootRegion },
      { at: volStart + 12 * SECTOR, bytes: bootRegion },
    ],
    fats: [{ start: fatStart, bytes: fatSectors * SECTOR }],
    fatEntry: i => (i === 0 ? 0xfffffff8 : i === 1 ? EXFAT_EOC : chainEntry(extents, i, EXFAT_EOC)),
    dataStart,
    dataEnd: dataStart + clusterCount * clusterSize,
    clusterSize,
    extents,
  });
  return { label, size, read };
}

// ---------------------------------------------------------------------------

// The build runs on the main thread; this many files already take seconds and hundreds of MB.
export const FOLDER_MAX_FILES = 100_000;
// The partition table and boot sectors count sectors in 32 bits.
const MAX_IMAGE_BYTES = 0xffffffff * SECTOR;

export function buildFolderImage(
  folderName: string,
  entries: FolderFile[],
  fs: FolderFs = "fat32",
): FolderImage {
  if (entries.length > FOLDER_MAX_FILES) {
    throw new Error(`The folder has more than ${FOLDER_MAX_FILES} files (${entries.length}).`);
  }
  const tree =
    fs === "exfat"
      ? buildTree(entries, Number.MAX_SAFE_INTEGER, exfatFold)
      : buildTree(entries, FAT32_MAX_FILE, s => s.toLowerCase());
  const id = randomId();
  const built =
    fs === "exfat" ? buildExfat(folderName, tree, id) : buildFat32(folderName, tree, id);
  if (built.size > MAX_IMAGE_BYTES) {
    throw new Error("The folder is too large for a disk image (the limit is 2 TiB).");
  }
  return {
    id,
    fs,
    label: built.label,
    size: built.size,
    files: tree.fileCount,
    dirs: tree.dirs.length - 1,
    dataBytes: tree.dataBytes,
    skipped: tree.skipped,
    renamed: tree.renamed,
    read: built.read,
  };
}
