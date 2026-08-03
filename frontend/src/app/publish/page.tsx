"use client";

import Link from "next/link";
import { Upload, ChevronRight } from "lucide-react";
import { useState } from "react";
import { useAuth } from "@/providers/AuthProvider";
import { useTranscripts } from "@/lib/queries/transcripts";
import TranscriptList from "@/components/transcript/TranscriptList";
import PublishImportDialog from "./PublishImportDialog";
import { GettingStarted, TeachingEmptyState, DataState } from "@/lib/ft-ui";
import { cn } from "@/lib/utils";

const LS_KEY = "peasant:publish:getting-started-dismissed";

const PUBLISH_STEPS = [
  {
    title: "run the setup wizard",
    body: "Interactive wizard that connects your GitHub account, discovers your coding agent transcripts, configures providers (Claude Code, OpenCode), and sets your redaction level.",
    command: "peasant kickstart",
  },
  {
    title: "push transcripts",
    body: "Pushes unpublished transcripts to the village with automatic redaction. Use --dry-run to preview what will be pushed, or --visibility public to override the default visibility.",
    command: "peasant village push",
  },
];

export default function PublishPage() {
  const { user, isLoggedIn } = useAuth();
  const [importOpen, setImportOpen] = useState(false);

  const { data: recentData } = useTranscripts(
    user
      ? { owner: user.github_username, limit: "5", sort: "recent" }
      : undefined
  );

  if (!isLoggedIn) {
    return (
      <div className="max-w-[1600px] mx-auto px-6 pt-6 pb-12 animate-fade-up">
        <DataState
          status="live"
          empty
          emptyState={
            <TeachingEmptyState
              title="sign in to publish"
              body="Connect your GitHub account to push transcripts to the village."
              privacy={null}
            />
          }
        />
      </div>
    );
  }

  return (
    <div className="max-w-[1600px] mx-auto px-6 pt-6 pb-12 flex flex-col gap-6 animate-fade-up">
      {/* Breadcrumb trail */}
      <nav aria-label="Breadcrumb" className="flex items-center gap-1 text-xs">
        <Link
          href="/"
          className="text-ink-3 hover:text-ink transition-colors focus-mono cursor-pointer"
        >
          Village
        </Link>
        <ChevronRight className="size-3 shrink-0 text-ink-4" />
        <span className="font-medium text-ink">Publish</span>
      </nav>

      {/* Page title block */}
      <div className="flex flex-col gap-1">
        <h1 className="font-[family-name:var(--font-display)] text-2xl tracking-tight text-ink">
          Publishing Dashboard
        </h1>
        <p className="text-sm text-ink-3">
          Push transcripts from the Peasant CLI to the village.
        </p>
      </div>

      {/* Getting Started — dismissible; persists via storageKey */}
      <GettingStarted
        title="getting started"
        steps={PUBLISH_STEPS}
        storageKey={LS_KEY}
      />

      {/* Recent Publishes */}
      <TranscriptList
        items={recentData?.transcripts ?? []}
        title="Recent Publishes"
        showOwnerActions
        hideOwner
        headerAside={
          <button
            type="button"
            onClick={() => setImportOpen(true)}
            className={cn(
              "inline-flex items-center gap-1.5 border border-rule bg-surface px-2.5 h-8",
              "text-[13px] font-medium text-ink",
              "hover:bg-surface-hover focus-mono transition-colors cursor-pointer"
            )}
          >
            <Upload className="size-3.5" />
            Import
          </button>
        }
        emptyState={
          <DataState
            status="live"
            empty
            emptyState={
              <TeachingEmptyState
                title="no transcripts published yet"
                body="Run the setup wizard and push command above to get your first transcript into the village."
                command="peasant village push"
              />
            }
          />
        }
      />

      <PublishImportDialog
        open={importOpen}
        onClose={() => setImportOpen(false)}
      />
    </div>
  );
}
