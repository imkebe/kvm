import { useSearchParams } from "react-router";

import { OsServicesGuide } from "@components/OsServicesGuideDialog";

// The OS Services install guide on a page of its own, opened in a new tab
// from the guide dialog. The query carries the version, the fingerprint and
// the open tabs.
export default function OsServicesGuideRoute() {
  const [query] = useSearchParams();
  return (
    <div className="min-h-screen bg-white dark:bg-slate-900">
      <div className="mx-auto max-w-3xl px-4 py-8">
        <OsServicesGuide
          version={query.get("version") ?? undefined}
          fingerprint={query.get("fingerprint") ?? ""}
          initialSystem={query.get("system") ?? undefined}
          initialArch={query.get("arch") ?? undefined}
        />
      </div>
    </div>
  );
}
