"use client";

import { Suspense, useRef, useState } from "react";
import { useSearchParams, useRouter } from "next/navigation";
import { Tabs } from "@peasant-labs/fairtrade/ui";
import { OnHead, OnPage } from "@/components/online/Page";
import { useExploreSearchFocus } from "@/lib/exploreSearch";
import ExplorePage from "@/app/explore/ExplorePage";
import CollectivesDiscovery from "./CollectivesDiscovery";

type DiscoverView = "transcripts" | "collectives";

/** One destination for finding transcripts and the collectives that hold them.
 * The existing reads and join consent remain owned by their respective views.
 * Opened views stay mounted so a filter or an open result survives switching. */
function DiscoverViews() {
  const params = useSearchParams();
  const router = useRouter();
  const requested: DiscoverView = params.get("view") === "transcripts" ? "transcripts" : "collectives";
  const [selection, setSelection] = useState({ requested, view: requested });
  const [visited, setVisited] = useState<ReadonlySet<DiscoverView>>(() => new Set([requested]));
  const root = useRef<HTMLDivElement>(null);
  useExploreSearchFocus(root);

  // Adjust before children render when browser navigation changes the URL.
  // The local selection keeps tab clicks immediate while the router commits.
  if (selection.requested !== requested) {
    setSelection({ requested, view: requested });
  }
  if (!visited.has(requested)) {
    setVisited(new Set(visited).add(requested));
  }

  const select = (id: string) => {
    if (id !== "transcripts" && id !== "collectives") return;
    setSelection({ requested, view: id });
    setVisited((prior) => prior.has(id) ? prior : new Set(prior).add(id));
    const query = new URLSearchParams(params.toString());
    query.set("view", id);
    router.replace(`/discover?${query}`, { scroll: false });
  };

  return (
    <OnPage testId="discover-page">
      <OnHead title="discover" />
      <div ref={root} className="on-discover-views">
        <Tabs
          aria-label="discover"
          value={selection.view}
          onChange={select}
          tabs={[
            { id: "transcripts", label: "transcripts", content: visited.has("transcripts") ? <ExplorePage embedded /> : null },
            { id: "collectives", label: "collectives", content: visited.has("collectives") ? <CollectivesDiscovery embedded /> : null },
          ]}
        />
      </div>
    </OnPage>
  );
}

export default function DiscoverPage() {
  return (
    <Suspense fallback={
      <OnPage testId="discover-page">
        <OnHead title="discover" />
        <p role="status" className="on-muted">loading discovery</p>
      </OnPage>
    }>
      <DiscoverViews />
    </Suspense>
  );
}
