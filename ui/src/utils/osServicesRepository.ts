// OS Services repositories, as the KVM reads them (os_services_repository.go):
// JetKVM's release server, a GitHub repository whose releases hold the
// files as assets (a file under <version>/ is an asset of the release
// tagged <version>, a top-level file one of the latest release), or any
// other URL laid out as JetKVM's server.
export const OFFICIAL_OS_SERVICES_REPOSITORY = "https://os-services.jetkvm.com";

export type OsServicesRepositoryKind = "official" | "github" | "custom";

export function osServicesRepositoryKind(repository?: string): OsServicesRepositoryKind {
  if (!repository || repository === OFFICIAL_OS_SERVICES_REPOSITORY) return "official";
  return /^https:\/\/(www\.)?github\.com\//i.test(repository) ? "github" : "custom";
}

// The URL of a repository file, by its path in JetKVM's layout.
export function osServicesRepositoryFile(repository: string | undefined, path: string) {
  const base = (repository || OFFICIAL_OS_SERVICES_REPOSITORY).replace(/\/+$/, "");
  const file = path.replace(/^\/+/, "");
  if (osServicesRepositoryKind(base) === "github") {
    return file.includes("/")
      ? `${base}/releases/download/${file}`
      : `${base}/releases/latest/download/${file}`;
  }
  return `${base}/${file}`;
}

// The KVM takes plain http only for a private or loopback IP address, as
// net.IP IsPrivate/IsLoopback (os_services_repository.go); https always.
export function osServicesRepositoryHttpAllowed(repository: string) {
  let url: URL;
  try {
    url = new URL(repository);
  } catch {
    return false;
  }
  if (url.protocol !== "http:") return true;
  const host = url.hostname.replace(/^\[|\]$/g, "").toLowerCase();
  const v4 = host.match(/^(\d+)\.(\d+)\.\d+\.\d+$/);
  if (v4) {
    const [a, b] = [Number(v4[1]), Number(v4[2])];
    return a === 10 || a === 127 || (a === 172 && b >= 16 && b <= 31) || (a === 192 && b === 168);
  }
  return host === "::1" || /^f[cd][0-9a-f]{0,2}:/.test(host);
}
