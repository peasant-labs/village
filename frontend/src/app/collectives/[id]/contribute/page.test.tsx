import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { afterEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { parse } from "yaml";
import type { Group } from "@/lib/types";
import { AuthProvider } from "@/providers/AuthProvider";
import { makeUser } from "@/test/mountedGroupRoute";
import GroupContributePage from "@/app/collectives/[id]/contribute/page";
import { profileOwner } from "@/test/publicOwnerFixture";
import { GROUP_ID, projectHashOf, transcriptIdOf, transcriptOf } from "@/lib/contribute/testTokens";

interface SessionToken {
  id: string;
  project: string;
  branch?: string;
  visibility?: string;
  title?: string | null;
  already_shared?: boolean;
}

interface FamilyToken {
  root: SessionToken;
  helpers?: SessionToken[];
}

interface ProjectToken {
  project: string;
  matching: number;
  selectable: number;
}

type SnapshotToken = string[] | { status: number; code: string };

interface PageCase {
  name: string;
  projects: ProjectToken[];
  searches?: Record<string, ProjectToken[]>;
  families?: Record<string, FamilyToken[]>;
  snapshot?: Record<string, SnapshotToken>;
  picker_failures?: number[];
  expect: Record<string, unknown>;
}

const fixtures = parse(readFileSync(resolve(process.cwd(), "src/testdata/paged-contribute-page.yaml"), "utf8")) as {
  cases: PageCase[];
};

const REQUIRED_CASES = [
  "sections-state-their-counts-before-any-session-loads",
  "opening-a-section-reads-only-that-project",
  "select-all-takes-the-snapshot-and-submits-it",
  "a-snapshot-over-the-size-limit-asks-to-narrow",
  "a-busy-snapshot-asks-to-retry",
  "a-private-selection-opens-the-consent-dialog",
  "a-helper-in-another-project-is-submitted-under-its-own-project",
  "a-single-helper-can-be-contributed-alone",
  "a-selection-outside-the-search-stays-and-is-named",
  "a-search-without-matches-says-so",
  "the-project-list-failing-offers-a-retry",
  "nothing-to-contribute-says-so",
];

{
  const names = fixtures.cases.map((c) => c.name);
  const missing = REQUIRED_CASES.filter((name) => !names.includes(name));
  if (missing.length > 0) throw new Error(`paged-contribute-page fixture is missing case(s): ${missing.join(", ")}`);
  const unexpected = names.filter((name) => !REQUIRED_CASES.includes(name));
  if (unexpected.length > 0) throw new Error(`paged-contribute-page fixture has case(s) no test reads: ${unexpected.join(", ")}`);
}

function caseNamed(name: string): PageCase {
  return fixtures.cases.find((c) => c.name === name)!;
}

function sessionOf(token: SessionToken): Record<string, unknown> {
  return {
    ...transcriptOf({
      id: token.id,
      project: token.project,
      visibility: token.visibility ?? "public",
      already_shared: token.already_shared,
      title: token.title,
    }),
    git_branch: token.branch ?? "main",
  };
}

function summaryOf(token: ProjectToken) {
  return {
    project_hash: projectHashOf(token.project),
    project_display_name: `project ${token.project.replace(/^p/, "").toLowerCase()}`,
    project_name_source: "remote",
    matching_count: token.matching,
    selectable_count: token.selectable,
  };
}

function makeGroup(id: string): Group {
  return {
    id,
    name: "paged collective",
    description: null,
    linked_github_org: null,
    display_members: true,
    transcript_deletion_policy: "user_choice",
    creator: profileOwner({ id: "someone-else", github_username: "collective-owner" }),
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    acceptance_mode: "open",
    data_access: "members_only",
    role: "member",
    member_since: "2026-01-01T00:00:00Z",
  };
}

interface RecordedRequest {
  method: string;
  path: string;
  params: URLSearchParams;
  body: unknown;
}

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });

/**
 * Stubs `fetch` for the real mounted route with the library the case
 * describes: the project list (by search text), each project's paged read,
 * each project's snapshot, and the batch-share POST. Every request is
 * recorded, so a test asserts what was asked for, not only how often.
 */
function installLibrary(testCase: PageCase): RecordedRequest[] {
  const requests: RecordedRequest[] = [];
  const sessions = new Map<string, SessionToken>();
  for (const families of Object.values(testCase.families ?? {})) {
    for (const family of families) for (const session of [family.root, ...(family.helpers ?? [])]) sessions.set(session.id, session);
  }
  const pickerFailures = [...(testCase.picker_failures ?? [])];
  const base = `/collectives/${GROUP_ID}`;

  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = new URL(String(input), "http://village.test");
      const method = (init?.method ?? "GET").toUpperCase();
      const body = typeof init?.body === "string" ? JSON.parse(init.body) : null;
      const path = url.pathname;
      const params = url.searchParams;
      requests.push({ method, path, params, body });

      if (path.endsWith("/auth/me")) return json(makeUser("viewer"));
      if (path.endsWith(`${base}/contributable/projects`)) {
        const failed = pickerFailures.shift();
        if (failed) return json({ error: "the project list could not be read" }, failed);
        const q = params.get("q");
        const projects = q ? (testCase.searches?.[q] ?? []) : testCase.projects;
        const matching = projects.reduce((sum, p) => sum + p.matching, 0);
        return json({
          has_more: false,
          matching_total: matching,
          selectable_total: projects.reduce((sum, p) => sum + p.selectable, 0),
          providers: matching > 0 ? [{ provider: "anthropic", count: matching }] : [],
          projects: projects.map(summaryOf),
        });
      }
      if (path.endsWith(`${base}/contributable`) && params.get("view") === "paged") {
        const token = Object.keys(testCase.families ?? {}).find((p) => projectHashOf(p) === params.get("project_hash"));
        const families = token ? testCase.families![token] : [];
        return json({
          group_id: GROUP_ID,
          has_more: false,
          items: families.map((family) => ({
            root: { transcript: sessionOf(family.root) },
            helpers: (family.helpers ?? []).map((helper) => ({ transcript: sessionOf(helper), matched: true })),
          })),
        });
      }
      if (path.endsWith(`${base}/contributable`) && params.get("view") === "snapshot") {
        const projectHash = params.get("project_hash")!;
        const token = Object.keys(testCase.snapshot ?? {}).find((p) => projectHashOf(p) === projectHash);
        const snapshot = token ? testCase.snapshot![token] : [];
        if (!Array.isArray(snapshot)) return json({ error: "refused", code: snapshot.code }, snapshot.status);
        const records = snapshot.map((id) => {
          const session = sessions.get(id)!;
          return {
            id: transcriptIdOf(id),
            title: session.title === undefined ? `session ${id.slice(1)}` : session.title,
            project_hash: projectHashOf(session.project),
            visibility: session.visibility ?? "public",
          };
        });
        return json({ group_id: GROUP_ID, project_hash: projectHash, count: records.length, records });
      }
      if (path.endsWith(base)) {
        return json({
          group: makeGroup(GROUP_ID),
          members: [{ user_id: "someone-else", role: "owner" }],
          transcripts: [],
          stats: {},
          models: [],
          contributors: [],
          can_read: true,
          your_role: "member",
        });
      }
      if (path.endsWith(`${base}/shares`) && method === "POST") {
        return json({
          project_hash: body.project_hash,
          shared: (body.transcript_ids as string[]).map((id) => ({ transcript_id: id, status: "approved" })),
          already_shared: [],
        });
      }
      throw new Error(`paged contribute fixture received an unexpected ${method} request to ${url.toString()}`);
    }),
  );
  return requests;
}

async function renderRoute() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await act(async () => {
    render(
      <QueryClientProvider client={client}>
        <AuthProvider>
          <GroupContributePage params={Promise.resolve({ id: GROUP_ID })} />
        </AuthProvider>
      </QueryClientProvider>,
    );
  });
}

/** The visible sentence of an element whose text React splits across nodes. */
const textOf = (element: HTMLElement) => element.textContent!.replace(/\s+/g, " ").trim();
const tallyText = () => textOf(screen.getByTestId("contribute-selection-tally"));
const section = (project: string) => screen.getByTestId(`contribute-project-${projectHashOf(project)}`);
const sessionRow = (id: string) => screen.getByTestId(`contribute-session-row-${transcriptIdOf(id)}`);
const checkboxOf = (row: HTMLElement) => row.querySelector<HTMLInputElement>('input[type="checkbox"]')!;
const pagedReads = (requests: RecordedRequest[]) => requests.filter((r) => r.params.get("view") === "paged");
const shareBodies = (requests: RecordedRequest[]) =>
  requests
    .filter((r) => r.path.endsWith("/shares"))
    .map((r) => r.body as { project_hash: string; transcript_ids: string[]; visibility_confirmed: boolean });

async function openSection(project: string) {
  await waitFor(() => expect(section(project)).toBeInTheDocument());
  const toggle = within(section(project)).getByRole("button", { name: `project ${project.slice(1).toLowerCase()}` });
  act(() => {
    fireEvent.click(toggle);
  });
}

async function selectAll(project: string) {
  await waitFor(() => expect(section(project)).toBeInTheDocument());
  await act(async () => {
    fireEvent.click(within(section(project)).getByRole("button", { name: /^select all \d+ selectable sessions/ }));
  });
}

async function search(text: string) {
  act(() => {
    fireEvent.change(screen.getByRole("textbox", { name: "search sessions" }), { target: { value: text } });
  });
  await waitFor(() => expect(screen.getByRole("textbox", { name: "search sessions" })).toHaveValue(text));
}

function expectBatches(requests: RecordedRequest[], byProject: Record<string, string[]>) {
  const bodies = shareBodies(requests);
  expect(bodies).toHaveLength(Object.keys(byProject).length);
  for (const [project, ids] of Object.entries(byProject)) {
    const sent = bodies.find((b) => b.project_hash === projectHashOf(project));
    expect(sent, `no batch was sent for ${project}`).toBeDefined();
    expect([...sent!.transcript_ids].sort()).toEqual(ids.map(transcriptIdOf).sort());
  }
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("the paged contribute route", () => {
  it("sections-state-their-counts-before-any-session-loads", async () => {
    const c = caseNamed("sections-state-their-counts-before-any-session-loads");
    const requests = installLibrary(c);
    await renderRoute();

    await waitFor(() => expect(screen.getByTestId("contribute-selection-tally")).toBeInTheDocument());
    await waitFor(() => expect(tallyText()).toBe(c.expect.tally));
    for (const [project, counts] of Object.entries(c.expect.counts as Record<string, string>)) {
      expect(textOf(screen.getByTestId(`contribute-project-counts-${projectHashOf(project)}`))).toBe(counts);
    }
    for (const [project, label] of Object.entries(c.expect.select_all as Record<string, string>)) {
      expect(within(section(project)).getByRole("button", { name: /^select all/ })).toHaveTextContent(label);
    }
    // Every section starts closed, so no project's sessions are read yet.
    expect(pagedReads(requests)).toHaveLength(0);
  });

  it("opening-a-section-reads-only-that-project", async () => {
    const c = caseNamed("opening-a-section-reads-only-that-project");
    const requests = installLibrary(c);
    await renderRoute();
    const project = c.expect.open as string;
    const hash = projectHashOf(project);

    await openSection(project);
    await waitFor(() => expect(screen.getByTestId(`contribute-project-loaded-${hash}`)).toBeInTheDocument());

    expect(pagedReads(requests).map((r) => r.params.get("project_hash"))).toEqual([hash]);
    expect(textOf(screen.getByTestId(`contribute-project-loaded-${hash}`))).toBe(c.expect.loaded);
    const branches = [...section(project).querySelectorAll<HTMLElement>(`[data-testid^="contribute-branch-row-${hash}-"]`)];
    expect(branches.map((row) => textOf(row))).toEqual((c.expect.branches as string[]).map((b) => `branch · ${b}`));

    const locked = sessionRow(c.expect.locked as string);
    expect(within(locked).getByText("already contributed")).toBeInTheDocument();
    expect(checkboxOf(locked)).toBeDisabled();
  });

  it("select-all-takes-the-snapshot-and-submits-it", async () => {
    const c = caseNamed("select-all-takes-the-snapshot-and-submits-it");
    const requests = installLibrary(c);
    await renderRoute();
    const hash = projectHashOf("pA");

    await selectAll("pA");
    await waitFor(() => expect(tallyText()).toBe(c.expect.tally_after_select_all));
    expect(requests.filter((r) => r.params.get("view") === "snapshot").map((r) => r.params.get("project_hash"))).toEqual([hash]);
    expect(textOf(screen.getByTestId(`contribute-project-counts-${hash}`))).toBe(c.expect.counts_after_select_all);
    // The snapshot alone made the selection: the section was never opened.
    expect(pagedReads(requests)).toHaveLength(0);

    act(() => {
      fireEvent.click(within(section("pA")).getByRole("button", { name: "deselect all" }));
    });
    expect(tallyText()).toBe(c.expect.tally_after_deselect_all);

    await selectAll("pA");
    await waitFor(() => expect(screen.getByRole("button", { name: /contribute 2 transcripts/ })).toBeEnabled());
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: /contribute 2 transcripts/ }));
    });
    await waitFor(() => expect(shareBodies(requests)).toHaveLength(1));
    expectBatches(requests, c.expect.requests_by_project as Record<string, string[]>);
  });

  it.each(["a-snapshot-over-the-size-limit-asks-to-narrow", "a-busy-snapshot-asks-to-retry"])("%s", async (name) => {
    const c = caseNamed(name);
    installLibrary(c);
    await renderRoute();

    await selectAll("pA");
    const notice = await screen.findByTestId(`contribute-project-notice-${projectHashOf("pA")}`);
    expect(textOf(notice)).toBe(c.expect.notice);
    expect(tallyText()).toBe(c.expect.tally);
  });

  it("a-private-selection-opens-the-consent-dialog", async () => {
    const c = caseNamed("a-private-selection-opens-the-consent-dialog");
    const requests = installLibrary(c);
    await renderRoute();
    const expected = c.expect.request as { project: string; ids: string[]; visibility_confirmed: boolean };

    await openSection(expected.project);
    await waitFor(() => expect(sessionRow(c.expect.tick as string)).toBeInTheDocument());
    act(() => {
      fireEvent.click(checkboxOf(sessionRow(c.expect.tick as string)));
    });
    act(() => {
      fireEvent.click(screen.getByRole("button", { name: /contribute 1 transcript/ }));
    });

    // The dialog opens BEFORE anything is sent.
    expect(await screen.findByText(new RegExp(c.expect.dialog as string))).toBeInTheDocument();
    expect(shareBodies(requests)).toHaveLength(0);

    act(() => {
      fireEvent.click(screen.getByRole("checkbox", { name: /i understand and consent/i }));
    });
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: /contribute & make visible/i }));
    });
    await waitFor(() => expect(shareBodies(requests)).toHaveLength(1));
    expect(shareBodies(requests)[0]).toEqual({
      project_hash: projectHashOf(expected.project),
      transcript_ids: expected.ids.map(transcriptIdOf),
      visibility_confirmed: expected.visibility_confirmed,
    });
  });

  it("a-helper-in-another-project-is-submitted-under-its-own-project", async () => {
    const c = caseNamed("a-helper-in-another-project-is-submitted-under-its-own-project");
    const requests = installLibrary(c);
    await renderRoute();
    const rootId = transcriptIdOf(c.expect.root as string);
    const helperId = c.expect.helper as string;

    await openSection("pA");
    await waitFor(() => expect(sessionRow(c.expect.root as string)).toBeInTheDocument());

    // The helper is folded behind the shared child-session control under its
    // root, not drawn as a second flat row.
    expect(screen.queryByTestId(`contribute-session-row-${transcriptIdOf(helperId)}`)).toBeNull();
    const chip = document.querySelector<HTMLElement>(`[data-parent-transcript-id="${rootId}"]`);
    expect(chip, "the helper sits behind the control under its root").not.toBeNull();
    expect(within(chip!).getByTestId("child-session-disclosure-label").textContent).toBe(c.expect.disclosure_label);

    act(() => {
      fireEvent.click(checkboxOf(sessionRow(c.expect.root as string)));
    });
    expect(tallyText()).toBe(c.expect.tally_after_tick);

    act(() => {
      fireEvent.click(within(chip!).getByTestId("child-session-disclosure-toggle"));
    });
    const helperRow = within(within(chip!).getByTestId("child-session-disclosure-rows")).getByTestId(
      `contribute-session-row-${transcriptIdOf(helperId)}`,
    );
    expect(checkboxOf(helperRow).checked, "ticking the root takes its helper").toBe(true);
    expect(textOf(helperRow)).toContain("in project b");

    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: /contribute 2 transcripts/ }));
    });
    const byProject = c.expect.requests_by_project as Record<string, string[]>;
    await waitFor(() => expect(shareBodies(requests)).toHaveLength(Object.keys(byProject).length));
    expectBatches(requests, byProject);
  });

  it("a-single-helper-can-be-contributed-alone", async () => {
    const c = caseNamed("a-single-helper-can-be-contributed-alone");
    const requests = installLibrary(c);
    await renderRoute();
    const rootId = transcriptIdOf(c.expect.root as string);
    const helperId = transcriptIdOf(c.expect.helper as string);

    await openSection("pA");
    await waitFor(() => expect(sessionRow(c.expect.root as string)).toBeInTheDocument());
    const chip = document.querySelector<HTMLElement>(`[data-parent-transcript-id="${rootId}"]`);
    expect(chip, "the helpers sit behind the control under their root").not.toBeNull();
    act(() => {
      fireEvent.click(within(chip!).getByTestId("child-session-disclosure-toggle"));
    });
    const helperRow = within(within(chip!).getByTestId("child-session-disclosure-rows")).getByTestId(
      `contribute-session-row-${helperId}`,
    );

    // Ticking one helper selects that helper and nothing else: not its root,
    // not its sibling.
    act(() => {
      fireEvent.click(checkboxOf(helperRow));
    });
    expect(tallyText()).toBe(c.expect.tally_after_tick);
    expect(checkboxOf(sessionRow(c.expect.root as string)).checked, "the root stays unticked").toBe(false);

    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: /contribute 1 transcript/ }));
    });
    const byProject = c.expect.requests_by_project as Record<string, string[]>;
    await waitFor(() => expect(shareBodies(requests)).toHaveLength(Object.keys(byProject).length));
    expectBatches(requests, byProject);
  });

  it("a-selection-outside-the-search-stays-and-is-named", async () => {
    const c = caseNamed("a-selection-outside-the-search-stays-and-is-named");
    const requests = installLibrary(c);
    await renderRoute();

    await selectAll("pA");
    await waitFor(() => expect(tallyText()).toMatch(/^2 selected/));

    await search(c.expect.search as string);
    await waitFor(() => expect(requests.some((r) => r.params.get("q") === c.expect.search)).toBe(true), { timeout: 2000 });
    const notice = await screen.findByTestId("contribute-unlisted-notice");
    expect(textOf(notice)).toContain(c.expect.notice as string);
    expect(tallyText()).toBe(c.expect.tally_while_searching);

    act(() => {
      fireEvent.click(within(notice).getByRole("button", { name: "deselect them" }));
    });
    expect(screen.queryByTestId("contribute-unlisted-notice")).toBeNull();
    expect(tallyText()).toBe(c.expect.tally_after_deselect);
  });

  it("a-search-without-matches-says-so", async () => {
    const c = caseNamed("a-search-without-matches-says-so");
    installLibrary(c);
    await renderRoute();

    await waitFor(() => expect(section("pA")).toBeInTheDocument());
    await search(c.expect.search as string);
    expect(await screen.findByText(c.expect.empty as string, {}, { timeout: 2000 })).toBeInTheDocument();
    // A search that matches nothing still offers the search back, not the
    // empty-library state.
    expect(screen.getByRole("textbox", { name: "search sessions" })).toBeInTheDocument();
  });

  it("the-project-list-failing-offers-a-retry", async () => {
    const c = caseNamed("the-project-list-failing-offers-a-retry");
    const requests = installLibrary(c);
    await renderRoute();
    const pickerReads = () => requests.filter((r) => r.path.endsWith("/contributable/projects"));

    const retry = await screen.findByRole("button", { name: "retry your transcripts" });
    expect(screen.getAllByRole("heading", { level: 1 })).toHaveLength(1);
    for (let read = 1; read <= c.picker_failures!.length; read += 1) {
      expect(screen.getByRole("alert")).toHaveTextContent("your transcripts could not load");
      expect(screen.queryByText(/no transcripts to contribute/)).not.toBeInTheDocument();
      retry.focus();
      fireEvent.click(retry);
      await waitFor(() => expect(pickerReads()).toHaveLength(read + 1));
      if (read < c.picker_failures!.length) {
        await waitFor(() => expect(retry).not.toHaveAttribute("aria-disabled", "true"));
        expect(retry).toHaveFocus();
      }
    }
    for (const project of c.expect.projects as string[]) {
      expect(await screen.findByTestId(`contribute-project-${projectHashOf(project)}`)).toBeInTheDocument();
    }
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    expect(requests.filter((r) => r.method === "POST")).toHaveLength(0);
  });

  it("nothing-to-contribute-says-so", async () => {
    const c = caseNamed("nothing-to-contribute-says-so");
    installLibrary(c);
    await renderRoute();

    expect(await screen.findByText(c.expect.title as string)).toBeInTheDocument();
    expect(screen.queryByTestId("contribute-selection-tally")).toBeNull();
    expect(screen.getByRole("button", { name: /contribute 0 transcripts/ })).toBeDisabled();
  });
});
