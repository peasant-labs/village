"use client";

import { useEffect, useMemo, useState } from "react";
import Link from "next/link";
import { Building2, CircleAlert, Compass, DoorOpen, Search, ShieldCheck, X } from "lucide-react";
import type { VillageCollectiveSearchResult } from "@peasant-labs/schema";
import { useJoinGroup, useSearchCollectives, useVisibleGroups } from "@/lib/queries/groups";
import { useMyCollectiveContributions } from "@/lib/queries/collectives";
import { useAuth } from "@/providers/AuthProvider";
import { collectiveStanding, contributedCollectiveIds } from "@/lib/collectiveBadges";
import { counted, whoCanJoin } from "@/lib/collectivePolicyCopy";
import { loginHref } from "@/lib/signIn";
import type { VisibleGroup } from "@/lib/types";
import { OnEmpty, OnPage } from "@/components/online/Page";
import NewCollectiveDialog from "@/components/collectives/NewCollectiveDialog";
import JoinConsentDialog from "@/components/group/JoinConsentDialog";
import "../online-collectives.css";

/** How long typing settles before the search is asked. */
const SEARCH_SETTLE_MS = 250;

/**
 * One card: what the search or the visible list answered, and, when the
 * collective is in the visible list, how it is joined and the viewer's role.
 * A search result the visible list does not hold claims neither.
 */
interface CardData {
  id: string;
  name: string;
  description: string | null;
  linkedOrg: string | null;
  memberCount: number;
  transcriptCount: number;
  mode: string | null;
  role: string | null;
  contributed: boolean;
}

function fromVisible(group: VisibleGroup, contributed: ReadonlySet<string>): CardData {
  const standing = collectiveStanding(group, contributed);
  return {
    id: group.id,
    name: group.name,
    description: group.description,
    linkedOrg: group.linked_github_org,
    memberCount: group.member_count,
    transcriptCount: group.transcript_count,
    mode: group.acceptance_mode,
    role: standing.memberRole,
    contributed: standing.hasContributed,
  };
}

function fromSearch(
  result: VillageCollectiveSearchResult,
  visible: ReadonlyMap<string, VisibleGroup>,
  contributed: ReadonlySet<string>,
): CardData {
  const known = visible.get(result.id);
  if (known) return { ...fromVisible(known, contributed), memberCount: result.member_count, transcriptCount: result.transcript_count };
  return {
    id: result.id,
    name: result.name,
    description: result.description,
    linkedOrg: result.linked_github_org,
    memberCount: result.member_count,
    transcriptCount: result.transcript_count,
    mode: null,
    role: null,
    contributed: contributed.has(result.id),
  };
}

function JoinIcon({ mode }: { mode: string }) {
  if (mode === "open") return <DoorOpen aria-hidden="true" />;
  if (mode === "verified_only") return <Building2 aria-hidden="true" />;
  return <ShieldCheck aria-hidden="true" />;
}

function SkeletonCards() {
  return (
    <ul className="on-cards" aria-busy="true" aria-label="loading collectives">
      {[0, 1, 2].map((n) => (
        <li key={n} className="on-skel is-card" />
      ))}
    </ul>
  );
}

/**
 * Collectives a person can join: with no search, the collectives they may see
 * and have not joined; with a search, what the search route answers. Joining
 * uses the collective page's own route and consent step.
 */
export default function DiscoverPage() {
  const { isLoading: authLoading, isLoggedIn, user } = useAuth();
  const visible = useVisibleGroups();
  const { data: contributions } = useMyCollectiveContributions(isLoggedIn);
  const joinGroup = useJoinGroup();

  const [text, setText] = useState("");
  const [query, setQuery] = useState("");
  const search = useSearchCollectives(query);
  const [creating, setCreating] = useState(false);
  const [joiningId, setJoiningId] = useState<string | null>(null);
  const [joined, setJoined] = useState<ReadonlySet<string>>(() => new Set());
  const [joinErrors, setJoinErrors] = useState<Readonly<Record<string, string>>>({});
  const [consentFor, setConsentFor] = useState<CardData | null>(null);

  // The search follows the box once typing settles; Enter asks at once.
  useEffect(() => {
    const next = text.trim();
    if (next === query) return;
    const timer = setTimeout(() => setQuery(next), next ? SEARCH_SETTLE_MS : 0);
    return () => clearTimeout(timer);
  }, [text, query]);

  const contributed = useMemo(() => contributedCollectiveIds(contributions), [contributions]);
  const visibleById = useMemo(() => new Map((visible.data ?? []).map((g) => [g.id, g])), [visible.data]);

  const searching = query.length > 0;
  const cards: CardData[] = useMemo(() => {
    if (searching) return (search.data?.collectives ?? []).map((r) => fromSearch(r, visibleById, contributed));
    // A collective joined from this page stays where it was, now saying so.
    return (visible.data ?? [])
      .filter((g) => !g.role?.trim() || joined.has(g.id))
      .map((g) => fromVisible(g, contributed));
  }, [searching, search.data, visible.data, visibleById, contributed, joined]);

  const join = (card: CardData) => {
    setJoinErrors((errors) => {
      const rest = { ...errors };
      delete rest[card.id];
      return rest;
    });
    setJoiningId(card.id);
    joinGroup.mutate(card.id, {
      onSuccess: () => {
        setJoined((ids) => new Set(ids).add(card.id));
        setConsentFor(null);
      },
      onError: (error) => {
        setJoinErrors((errors) => ({ ...errors, [card.id]: error.message }));
        setConsentFor(null);
      },
      onSettled: () => setJoiningId(null),
    });
  };

  const askToJoin = (card: CardData) => {
    if (user && !user.is_discoverable) {
      setConsentFor(card);
      return;
    }
    join(card);
  };

  const head = (
    <header className="on-split">
      <h1 className="on-title">discover</h1>
      <p>Collectives you can join. Search by name or GitHub org.</p>
    </header>
  );

  if (!authLoading && !isLoggedIn) {
    return (
      <OnPage testId="discover-page">
        {head}
        <OnEmpty
          testId="discover-signed-out"
          icon={<Compass />}
          title="sign in to discover collectives"
          body="Find the teams, labs and classes you can join."
          actions={
            <Link className="btn btn-primary" href={loginHref("/discover")}>
              sign in
            </Link>
          }
        />
      </OnPage>
    );
  }

  const source = searching ? search : visible;
  const loading = authLoading || source.isLoading || (searching && visible.isLoading);
  const failed = !loading && source.isError;

  const clearSearch = () => {
    setText("");
    setQuery("");
  };

  let body;
  if (loading) {
    body = <SkeletonCards />;
  } else if (failed) {
    body = (
      <div className="on-notice" data-tone="danger" role="alert">
        <p>
          {searching ? "the search failed" : "could not read the collectives you can join"}: {source.error?.message}
        </p>
        <button type="button" className="btn btn-secondary" disabled={source.isFetching} onClick={() => void source.refetch()}>
          try again
        </button>
      </div>
    );
  } else if (cards.length === 0 && searching) {
    body = (
      <OnEmpty
        testId="discover-no-match"
        icon={<Search />}
        title="no collective matches"
        body={<>Nothing is named or linked to “{query}”. Try another name or GitHub org.</>}
        actions={
          <button type="button" className="btn btn-secondary" onClick={clearSearch}>
            clear search
          </button>
        }
      />
    );
  } else if (cards.length === 0) {
    body = (
      <OnEmpty
        testId="discover-empty"
        icon={<Compass />}
        title="nothing to discover yet"
        body="You belong to every collective you can see. Search by name or GitHub org, or start your own."
        actions={
          <>
            <button type="button" className="btn btn-primary" onClick={() => setCreating(true)}>
              new collective
            </button>
            <Link className="btn btn-secondary" href="/collectives">
              your collectives
            </Link>
          </>
        }
      />
    );
  } else {
    body = (
      <ul className="on-cards is-collectives" aria-label={searching ? `collectives matching ${query}` : "collectives you can join"}>
        {cards.map((card) => {
          const titleId = `discover-${card.id}`;
          const justJoined = joined.has(card.id);
          const error = joinErrors[card.id];
          const busy = joiningId === card.id;
          let action;
          if (card.role || justJoined) {
            action = (
              <Link
                className={justJoined ? "btn btn-primary" : "btn btn-secondary"}
                href={`/collectives/${card.id}`}
                aria-label={`open ${card.name}`}
              >
                open
              </Link>
            );
          } else if (card.mode === "open" || card.mode === "verified_only") {
            action = (
              <button
                type="button"
                className={card.mode === "open" ? "btn btn-primary" : "btn btn-secondary"}
                aria-label={`join ${card.name}`}
                disabled={joiningId !== null}
                aria-busy={busy || undefined}
                onClick={() => askToJoin(card)}
              >
                {busy ? "joining" : "join"}
              </button>
            );
          } else {
            action = (
              <Link className="btn btn-secondary" href={`/collectives/${card.id}`} aria-label={`view ${card.name}`}>
                view
              </Link>
            );
          }
          const who = card.mode ? whoCanJoin(card.mode, card.linkedOrg) : "";
          const role = justJoined ? null : card.role;
          return (
            <li key={card.id}>
              <article className="on-card" aria-labelledby={titleId} data-collective-id={card.id}>
                <div className="on-card-head">
                  <h2 id={titleId}>
                    <Link href={`/collectives/${card.id}`}>{card.name}</Link>
                  </h2>
                  <span className="on-chrome is-n">{counted(card.memberCount, "member")}</span>
                </div>
                {card.description ? <p className="on-card-desc">{card.description}</p> : null}
                {who && card.mode ? (
                  <div className="on-card-who">
                    <JoinIcon mode={card.mode} />
                    {who}
                  </div>
                ) : null}
                {role || justJoined || card.contributed ? (
                  <div className="on-card-tags" data-card="standing">
                    {justJoined ? (
                      <span className="on-tag" data-tone="ok">
                        joined
                      </span>
                    ) : null}
                    {role ? <span className="on-tag">{role}</span> : null}
                    {card.contributed ? <span className="on-tag">contributed</span> : null}
                  </div>
                ) : null}
                {error ? (
                  <p className="on-card-msg" role="alert">
                    <CircleAlert aria-hidden="true" />
                    <span>could not join: {error}</span>
                  </p>
                ) : null}
                <div className="on-card-foot">
                  <span className="on-chrome is-n">{counted(card.transcriptCount, "transcript")}</span>
                  {action}
                </div>
              </article>
            </li>
          );
        })}
      </ul>
    );
  }

  return (
    <OnPage testId="discover-page">
      {head}
      <div className="on-tools" role="search">
        <div className="on-search is-wide is-clearable">
          <Search aria-hidden="true" />
          <input
            type="search"
            aria-label="find a collective"
            placeholder="collective name or github org"
            value={text}
            onChange={(event) => setText(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === "Enter") {
                event.preventDefault();
                setQuery(text.trim());
              } else if (event.key === "Escape" && text) {
                event.preventDefault();
                clearSearch();
              }
            }}
          />
          {text ? (
            <button type="button" className="on-icon-btn" aria-label="clear search" onClick={clearSearch}>
              <X aria-hidden="true" />
            </button>
          ) : null}
        </div>
      </div>
      <div aria-live="polite" className="sr-only">
        {searching && !loading && !failed ? `${cards.length} ${cards.length === 1 ? "match" : "matches"}` : ""}
      </div>
      {body}
      {consentFor ? (
        <JoinConsentDialog
          open
          collectiveName={consentFor.name}
          isSubmitting={joiningId === consentFor.id}
          onClose={() => setConsentFor(null)}
          onConfirm={() => join(consentFor)}
        />
      ) : null}
      {creating && user ? <NewCollectiveDialog onClose={() => setCreating(false)} /> : null}
    </OnPage>
  );
}
