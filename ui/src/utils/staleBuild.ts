// After the KVM gets a new app, an open tab still runs the old page, whose
// code-split chunks (named by content hash) are gone: every page that has not
// been opened yet fails with "error loading dynamically imported module".
// Reloading fetches the new page. At most once a minute, so a KVM that is
// really unreachable does not make the tab reload in a loop.

const KEY = "jetkvm-stale-build-reload";
let reloadedHere = false;

export function isStaleChunkError(error: unknown) {
  const message = error instanceof Error ? error.message : typeof error === "string" ? error : "";
  return /dynamically imported module|Importing a module script failed|Unable to preload CSS/i.test(
    message,
  );
}

// Reloads the page unless that happened less than a minute ago; says whether
// it reloads.
export function reloadForNewBuild() {
  if (reloadedHere) return true;
  try {
    const last = Number(sessionStorage.getItem(KEY) ?? 0);
    if (Date.now() - last < 60_000) return false;
    sessionStorage.setItem(KEY, String(Date.now()));
  } catch {
    // No session storage: reload this once.
  }
  reloadedHere = true;
  window.location.reload();
  return true;
}
