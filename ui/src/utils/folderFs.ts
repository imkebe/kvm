import { FAT32_MAX_FILE, type FolderFile, type FolderFs } from "@/utils/folderImage";

// Which file system a folder disk should use for the target computer. OS
// Services tells the target's system (host info, else the clipboard sender);
// without it the target is unknown.

export interface TargetSystem {
  family: string; // "windows", "macos", "linux", "freebsd", … or "" when unknown
  name: string;
  kernel: string;
}

export type FsReason =
  | "uefi"
  | "windows"
  | "macos"
  | "linux"
  | "linux_old"
  | "other"
  | "unknown"
  | "large_files";

export interface FsProposal {
  fs: FolderFs;
  reason: FsReason;
}

export interface FolderTraits {
  // EFI/BOOT/ is there: firmware boots only from FAT.
  efiBoot: boolean;
  largestFile: number;
}

export function folderTraits(entries: FolderFile[]): FolderTraits {
  let efiBoot = false;
  let largestFile = 0;
  for (const entry of entries) {
    if (/^efi\/boot\//i.test(entry.path)) efiBoot = true;
    largestFile = Math.max(largestFile, entry.file.size);
  }
  return { efiBoot, largestFile };
}

// exFAT is in the Linux kernel from 5.7 on.
function linuxHasExfat(kernel: string) {
  const match = /^(\d+)\.(\d+)/.exec(kernel);
  if (!match) return false;
  const major = Number(match[1]);
  const minor = Number(match[2]);
  return major > 5 || (major === 5 && minor >= 7);
}

export function proposeFolderFs(target: TargetSystem | null, traits: FolderTraits): FsProposal {
  if (traits.efiBoot) return { fs: "fat32", reason: "uefi" };
  switch (target?.family) {
    case "windows":
      return { fs: "exfat", reason: "windows" };
    case "macos":
      return { fs: "exfat", reason: "macos" };
    case "linux":
      return linuxHasExfat(target.kernel)
        ? { fs: "exfat", reason: "linux" }
        : { fs: "fat32", reason: "linux_old" };
    case undefined:
    case "":
      return traits.largestFile > FAT32_MAX_FILE
        ? { fs: "exfat", reason: "large_files" }
        : { fs: "fat32", reason: "unknown" };
    default:
      return { fs: "fat32", reason: "other" };
  }
}

const FAMILY_NAMES: Record<string, string> = {
  windows: "Windows",
  macos: "macOS",
  linux: "Linux",
  freebsd: "FreeBSD",
  netbsd: "NetBSD",
  openbsd: "OpenBSD",
};

export function targetSystem(
  hostOs: { family?: string; name?: string; version?: string; kernel?: string } | undefined,
  clipboardOs: string | undefined,
): TargetSystem | null {
  if (hostOs?.family) {
    const product = [hostOs.name, hostOs.version].filter(Boolean).join(" ");
    return {
      family: hostOs.family,
      name: product || FAMILY_NAMES[hostOs.family] || hostOs.family,
      kernel: hostOs.kernel ?? "",
    };
  }
  if (clipboardOs && clipboardOs !== "unknown") {
    return { family: clipboardOs, name: FAMILY_NAMES[clipboardOs] ?? clipboardOs, kernel: "" };
  }
  return null;
}
