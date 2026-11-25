import { test, expect } from "bun:test";
import { buildFolderImage, FOLDER_MAX_FILES, type FolderFile } from "../src/utils/folderImage";

const file = (size: number): FolderFile["file"] => ({
  size,
  lastModified: 1_700_000_000_000,
  slice: (a = 0, b = size) => new Blob([new Uint8Array(Math.max(0, Math.min(b, size) - a))]),
});
const small: FolderFile[] = [
  { path: "hello.txt", file: file(5) },
  { path: "dir/inner.bin", file: file(70_000) },
];
const text = (b: Uint8Array, at: number, n: number) =>
  new TextDecoder().decode(b.subarray(at, at + n));
const START = 2048 * 512;

test("FAT32: MBR and boot sector fields", async () => {
  const image = buildFolderImage("TEST", small, "fat32");
  expect(image.files).toBe(2);
  expect(image.size % 4096).toBe(0);
  const mbr = await image.read(0, 512);
  const mv = new DataView(mbr.buffer);
  expect(mv.getUint16(510, true)).toBe(0xaa55);
  expect(mbr[446 + 4]).toBe(0x0c);
  expect(mv.getUint32(446 + 8, true)).toBe(2048);
  const sectors = mv.getUint32(446 + 12, true);
  expect(sectors * 512 + START).toBeLessThanOrEqual(image.size);
  const boot = await image.read(START, 512);
  const bv = new DataView(boot.buffer);
  expect(bv.getUint16(11, true)).toBe(512);
  expect(bv.getUint32(32, true)).toBe(sectors);
  expect(text(boot, 82, 5)).toBe("FAT32");
  expect(bv.getUint16(510, true)).toBe(0xaa55);
});

test("exFAT: boot sector fields", async () => {
  const image = buildFolderImage("TEST", small, "exfat");
  const mbr = await image.read(0, 512);
  const mv = new DataView(mbr.buffer);
  expect(mv.getUint16(510, true)).toBe(0xaa55);
  expect(mbr[446 + 4]).toBe(0x07);
  const boot = await image.read(START, 512);
  const bv = new DataView(boot.buffer);
  expect(text(boot, 3, 8)).toBe("EXFAT   ");
  expect(boot[108]).toBe(9);
  expect(bv.getUint32(92, true)).toBeGreaterThan(0);
  expect(bv.getUint16(510, true)).toBe(0xaa55);
});

test("a folder over 2 TiB is refused, not wrapped", () => {
  expect(() =>
    buildFolderImage("BIG", [{ path: "big.bin", file: file(3 * 2 ** 40) }], "exfat"),
  ).toThrow(/2 TiB/);
});

test("too many files is refused with a message", () => {
  const many = Array.from({ length: FOLDER_MAX_FILES + 1 }, (_, i) => ({
    path: `f${i}`,
    file: file(0),
  }));
  expect(() => buildFolderImage("MANY", many, "exfat")).toThrow(/more than/);
});
