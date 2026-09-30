import { Suspense, type ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, render } from "@testing-library/react";
import { afterEach, vi } from "vitest";
import PullRequestPage from "@/app/pulls/[owner]/[name]/[number]/page";
import type { PromptDigest, VillagePullRequestAttachmentResponse } from "@peasant-labs/schema";

/**
 * Mount support for the real pull request page, with REST stubbed at `fetch`
 * and every outbound request recorded so a test asserts the route a control
 * hit rather than inspecting a mutation object.
 *
 * The pull request page reads everything it needs from its own payload, so it
 * mounts without an auth provider. The collective settings page mounts through
 * `src/test/mountedCollectivePages.tsx`.
 */

/** One recorded outbound request. */
export interface RecordedRequest {
  method: string;
  url: string;
  body: unknown;
}

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "content-type": "application/json" },
  });

function QueryOnly({ children }: { children: ReactNode }) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return (
    <QueryClientProvider client={client}>
      <Suspense fallback={<div>loading</div>}>{children}</Suspense>
    </QueryClientProvider>
  );
}

// ---------------------------------------------------------------------------
// Pull request page
// ---------------------------------------------------------------------------

export interface PullRequestFixture {
  owner: string;
  name: string;
  number: number;
  viewerIsAuthor: boolean;
  attachment: VillagePullRequestAttachmentResponse["attachment"];
  digest: PromptDigest | null;
  transcripts: VillagePullRequestAttachmentResponse["transcripts"];
  /** Status a confirm answers; 200 (or unset) returns the attached response. */
  confirmStatus?: number;
  confirmMessage?: string;
}

/**
 * A minimal but complete digest, so every branch of the renderer is exercised.
 * It advertises one transcript per id it is given, because the page decides a
 * row is "not available" by asking whether the digest still carries its id.
 */
export function makeDigest(
  transcriptIds: string[] = ["11111111-1111-1111-1111-111111111111"],
): PromptDigest {
  const timestamp = "2026-01-01T00:00:00Z";
  const items: PromptDigest["items"] = [];
  transcriptIds.forEach((transcriptId, index) => {
    items.push(
      { kind: "session", transcriptId, timestamp, text: `session ${index + 1}`, promptCount: 2, commitCount: 1 },
      {
        kind: "prompt",
        transcriptId,
        timestamp,
        text: index === 0 ? "please add the picker" : `prompt for ${transcriptId}`,
        ordinal: 1,
        turnIndex: 0,
      },
      { kind: "skill", transcriptId, timestamp, text: "/commit", args: "-m seed", turnIndex: 1 },
      {
        kind: "commit",
        transcriptId,
        timestamp,
        text: "abc1234",
        commitSha: "abc1234000000000000000000000000000000001",
        additions: 3,
        deletions: 1,
        filesChanged: 2,
      },
    );
  });
  return {
    header: {
      sessionCount: transcriptIds.length,
      promptCount: 2,
      commitsCovered: 1,
      commitsTotal: 2,
      harness: "claude-code",
      redactionLevel: "standard",
      villageUrl: "https://village.test",
    },
    skills: [{ name: "commit", invocationCount: 1 }],
    items,
  };
}

export function makeAttachmentResponse(fixture: PullRequestFixture): VillagePullRequestAttachmentResponse {
  return {
    attachment: fixture.attachment,
    digest: fixture.digest,
    transcripts: fixture.transcripts,
    viewer_is_author: fixture.viewerIsAuthor,
  };
}

export function installPullRequestREST(fixture: PullRequestFixture): RecordedRequest[] {
  const requests: RecordedRequest[] = [];
  let attachment = { ...fixture.attachment };
  const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    const method = (init?.method ?? "GET").toUpperCase();
    let body: unknown = null;
    if (typeof init?.body === "string") {
      try {
        body = JSON.parse(init.body);
      } catch {
        body = init.body;
      }
    }
    requests.push({ method, url, body });

    if (method === "POST" && url.endsWith("/confirm")) {
      if (fixture.confirmStatus && fixture.confirmStatus >= 400) {
        return json({ error: fixture.confirmMessage ?? "refused" }, fixture.confirmStatus);
      }
      attachment = { ...attachment, state: "attached" };
      return json(makeAttachmentResponse({ ...fixture, attachment }));
    }
    if (method === "DELETE") {
      attachment = { ...attachment, state: "detached" };
      return json(makeAttachmentResponse({ ...fixture, attachment }));
    }
    return json(makeAttachmentResponse({ ...fixture, attachment }));
  });
  vi.stubGlobal("fetch", fetchMock);
  return requests;
}

export async function renderPullRequestRoute(owner: string, name: string, number: number): Promise<void> {
  await act(async () => {
    render(
      <QueryOnly>
        <PullRequestPage params={Promise.resolve({ owner, name, number: String(number) })} />
      </QueryOnly>,
    );
  });
}

export function installAttachmentSurfacesTeardown(): void {
  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
    globalThis.localStorage?.clear();
    document.documentElement.setAttribute("data-theme", "dark");
  });
}
