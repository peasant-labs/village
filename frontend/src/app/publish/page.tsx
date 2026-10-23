"use client";

import Link from "next/link";
import { Upload } from "lucide-react";
import { useState } from "react";
import { useAuth } from "@/providers/AuthProvider";
import { OnCrumb, OnEmpty, OnHead, OnPage } from "@/components/online/Page";
import { HOME_SECTION } from "@/lib/nav/sections";
import { loginHref } from "@/lib/signIn";
import { useTranscripts } from "@/lib/queries/transcripts";
import TranscriptList from "@/components/transcript/TranscriptList";
import PublishImportDialog from "./PublishImportDialog";
import { GettingStarted, TeachingEmptyState, DataState } from "@/lib/ft-ui";
import "@/app/online-pages.css";

const LS_KEY = "peasant:publish:getting-started-dismissed";

// Publishing grants a license, so the notice that states each grant is linked
// from this page in every auth state, before a person runs the push command.
function PrivacyNoticeLine() {
  // One element, not a fragment: the empty state's privacy slot is a flex
  // row, and a fragment would hand it three items to wrap independently.
  return (
    <span>
      publishing grants a license. read the{" "}
      <Link
        href="/privacy"
        className="text-ink underline decoration-ink-4 underline-offset-2 hover:decoration-ink focus-mono"
      >
        privacy notice
      </Link>{" "}
      before you push.
    </span>
  );
}

const PUBLISH_STEPS = [
  {
    title: "run the setup wizard",
    body: "interactive wizard that connects your github account, discovers your coding agent transcripts, configures providers (claude code, opencode), and sets your redaction level.",
    command: "peasant kickstart",
  },
  {
    title: "push transcripts",
    body: "pushes unpublished transcripts to peasant online with automatic redaction. use --dry-run to preview what will be pushed, or --visibility public to override the default visibility.",
    command: "peasant village push",
  },
];

export default function PublishPage() {
  const { user, isLoading, isLoggedIn } = useAuth();
  const [importOpen, setImportOpen] = useState(false);

  const { data: recentData } = useTranscripts(
    user
      ? { owner: user.github_username, limit: "5", sort: "recent" }
      : undefined
  );

  // Nobody is asked to sign in before the session read has answered.
  if (isLoading) {
    return (
      <OnPage>
        <div className="h-10 w-72 animate-shimmer" aria-hidden="true" />
        <div className="h-48 animate-shimmer" aria-hidden="true" />
      </OnPage>
    );
  }

  if (!isLoggedIn) {
    return (
      <OnPage>
        <OnEmpty
          icon={<Upload aria-hidden="true" />}
          title="sign in to publish"
          body="sign in with github to push transcripts from peasant to peasant online."
          actions={
            <Link href={loginHref("/publish")} className="btn btn-primary">
              sign in
            </Link>
          }
        >
          <p>
            <PrivacyNoticeLine />
          </p>
        </OnEmpty>
      </OnPage>
    );
  }

  return (
    <OnPage tight>
      <OnCrumb items={[{ label: HOME_SECTION.label, href: HOME_SECTION.href }, { label: "publish" }]} />

      <OnHead
        title="publishing dashboard"
        lede={
          <>
            push transcripts from the peasant cli to peasant online. <PrivacyNoticeLine />
          </>
        }
      />

      <div className="on-plain-sections">
        {/* Getting started: dismissible, and remembered through storageKey */}
        <GettingStarted
          title="getting started"
          steps={PUBLISH_STEPS}
          storageKey={LS_KEY}
        />

        {/* Recent Publishes */}
        <TranscriptList
          items={recentData?.transcripts ?? []}
          title="recent publishes"
          showOwnerActions
          hideOwner
          headerAside={
            <button
              type="button"
              onClick={() => setImportOpen(true)}
              className="btn btn-secondary btn-sm"
            >
              <Upload className="size-3.5" aria-hidden="true" />
              import
            </button>
          }
          emptyState={
            <DataState
              status="live"
              empty
              emptyState={
                <TeachingEmptyState
                  title="no transcripts published yet"
                  body="run the setup wizard and the push command above to get your first transcript onto peasant online."
                  command="peasant village push"
                />
              }
            />
          }
        />
      </div>

      <PublishImportDialog
        open={importOpen}
        onClose={() => setImportOpen(false)}
      />
    </OnPage>
  );
}
