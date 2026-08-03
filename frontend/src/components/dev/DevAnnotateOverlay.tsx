"use client";

import { useState, useEffect } from "react";
import { MessageSquarePlus } from "lucide-react";

/**
 * Dev-only UI feedback tool. Click the floating button, then click any
 * element on the page to capture its route / anchor / selector / HTML and
 * leave a comment. The comment is POSTed to `/api/dev-feedback`, which
 * appends it to `dev-feedback.md`.
 *
 * Styled in the monochrome-editorial system via design tokens (so it adapts
 * to light/dark), but rendered with inline styles so the overlay is immune
 * to app CSS and always sits above everything.
 */

type Mode = "idle" | "selecting" | "modal";

interface CapturedElement {
  route: string;
  anchor: string;
  selector: string;
  snippet: string;
}

function detectAnchor(el: Element): string {
  if (el.id) return `#${el.id}`;
  for (const attr of ["data-component", "data-testid"]) {
    const v = el.getAttribute(attr);
    if (v) return `[${attr}="${v}"]`;
  }
  const aria = el.getAttribute("aria-label");
  if (aria) return `aria-label="${aria}"`;
  if (el.matches("button, a")) {
    const text = (el.textContent ?? "").trim().slice(0, 60);
    if (text) return `${el.tagName.toLowerCase()}: "${text}"`;
  }
  if (el.matches("h1, h2, h3, h4, h5, h6")) {
    const text = (el.textContent ?? "").trim().slice(0, 60);
    return `${el.tagName.toLowerCase()}: "${text}"`;
  }
  let parent: Element | null = el.parentElement;
  let depth = 0;
  while (parent && depth < 6) {
    const h = parent.querySelector("h1, h2, h3, h4, h5, h6");
    if (h) return `in section: "${(h.textContent ?? "").trim().slice(0, 60)}"`;
    parent = parent.parentElement;
    depth++;
  }
  const cls = typeof el.className === "string" ? el.className.split(/\s+/)[0] : "";
  return cls ? `${el.tagName.toLowerCase()}.${cls}` : el.tagName.toLowerCase();
}

function buildSelector(el: Element): string {
  const parts: string[] = [];
  let cur: Element | null = el;
  let depth = 0;
  while (cur && depth < 4 && cur.tagName !== "BODY") {
    const node: Element = cur;
    if (node.id) {
      parts.unshift(`#${node.id}`);
      break;
    }
    const tag = node.tagName.toLowerCase();
    const parentEl: Element | null = node.parentElement;
    if (parentEl) {
      const children: Element[] = Array.from(parentEl.children);
      const sameTag: Element[] = children.filter((c) => c.tagName === node.tagName);
      const idx = sameTag.indexOf(node);
      parts.unshift(sameTag.length > 1 ? `${tag}:nth-of-type(${idx + 1})` : tag);
    } else {
      parts.unshift(tag);
    }
    cur = parentEl;
    depth++;
  }
  return parts.join(" > ");
}

const eyebrow: React.CSSProperties = {
  fontFamily: "var(--font-body)",
  fontSize: 11,
  fontWeight: 500,
  letterSpacing: "0.08em",
  textTransform: "uppercase",
  color: "hsl(var(--ink-3))",
  marginBottom: 4,
};

export function DevAnnotateOverlay() {
  const [mode, setMode] = useState<Mode>("idle");
  const [hoveredEl, setHoveredEl] = useState<Element | null>(null);
  const [captured, setCaptured] = useState<CapturedElement | null>(null);
  const [comment, setComment] = useState("");
  const [saving, setSaving] = useState(false);
  const [toast, setToast] = useState<string | null>(null);

  useEffect(() => {
    if (mode !== "selecting") return;
    document.body.style.cursor = "crosshair";

    const onMouseOver = (e: MouseEvent) => {
      const target = e.target as Element | null;
      if (!target || target.closest("[data-dev-overlay]")) {
        setHoveredEl(null);
        return;
      }
      setHoveredEl(target);
    };

    const onClick = (e: MouseEvent) => {
      const target = e.target as Element | null;
      if (!target || target.closest("[data-dev-overlay]")) return;
      e.preventDefault();
      e.stopPropagation();
      e.stopImmediatePropagation();
      setCaptured({
        route: window.location.pathname,
        anchor: detectAnchor(target),
        selector: buildSelector(target),
        snippet: target.outerHTML.slice(0, 400),
      });
      setMode("modal");
      setHoveredEl(null);
    };

    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        setMode("idle");
        setHoveredEl(null);
      }
    };

    const onScroll = () => setHoveredEl(null);

    document.addEventListener("mouseover", onMouseOver);
    document.addEventListener("click", onClick, true);
    document.addEventListener("keydown", onKey);
    window.addEventListener("scroll", onScroll, true);

    return () => {
      document.body.style.cursor = "";
      document.removeEventListener("mouseover", onMouseOver);
      document.removeEventListener("click", onClick, true);
      document.removeEventListener("keydown", onKey);
      window.removeEventListener("scroll", onScroll, true);
    };
  }, [mode]);

  useEffect(() => {
    if (mode !== "modal") return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        setMode("idle");
        setCaptured(null);
        setComment("");
      }
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [mode]);

  useEffect(() => {
    if (!toast) return;
    const t = setTimeout(() => setToast(null), 2500);
    return () => clearTimeout(t);
  }, [toast]);

  const cancel = () => {
    setMode("idle");
    setCaptured(null);
    setComment("");
  };

  const save = async () => {
    if (!captured || !comment.trim()) return;
    setSaving(true);
    try {
      const res = await fetch("/dev/feedback", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ ...captured, comment: comment.trim() }),
      });
      if (!res.ok) throw new Error(await res.text());
      setToast("Saved to dev-feedback.md");
      setMode("idle");
      setCaptured(null);
      setComment("");
    } catch (e) {
      setToast(`Error: ${e instanceof Error ? e.message : "save failed"}`);
    } finally {
      setSaving(false);
    }
  };

  const hoverRect = hoveredEl?.getBoundingClientRect();
  const canSave = comment.trim().length > 0 && !saving;

  return (
    <div data-dev-overlay>
      {mode === "selecting" && hoverRect && (
        <div
          style={{
            position: "fixed",
            top: hoverRect.top,
            left: hoverRect.left,
            width: hoverRect.width,
            height: hoverRect.height,
            border: "2px solid hsl(var(--mark))",
            background: "hsl(var(--ink) / 0.07)",
            pointerEvents: "none",
            zIndex: 9998,
          }}
        />
      )}

      {mode === "selecting" && (
        <div
          style={{
            position: "fixed",
            top: 12,
            left: "50%",
            transform: "translateX(-50%)",
            background: "hsl(var(--mark))",
            color: "hsl(var(--mark-fg))",
            padding: "7px 14px",
            borderRadius: 0,
            fontSize: 12,
            fontWeight: 500,
            zIndex: 10000,
            fontFamily: "var(--font-body)",
          }}
        >
          Click any element to leave a comment · Esc to cancel
        </div>
      )}

      {mode === "idle" && (
        <button
          onClick={() => setMode("selecting")}
          title="Add UI feedback (dev only)"
          aria-label="Add UI feedback"
          style={{
            position: "fixed",
            bottom: 16,
            right: 16,
            width: 44,
            height: 44,
            display: "flex",
            alignItems: "center",
            justifyContent: "center",
            borderRadius: 0,
            background: "hsl(var(--mark))",
            color: "hsl(var(--mark-fg))",
            border: "1px solid hsl(var(--rule))",
            cursor: "pointer",
            zIndex: 9997,
          }}
        >
          <MessageSquarePlus size={20} aria-hidden />
        </button>
      )}

      {mode === "modal" && captured && (
        <>
          <div
            onClick={cancel}
            style={{
              position: "fixed",
              inset: 0,
              background: "hsl(var(--ink) / 0.45)",
              zIndex: 9999,
            }}
          />
          <div
            role="dialog"
            aria-label="UI feedback"
            style={{
              position: "fixed",
              top: "50%",
              left: "50%",
              transform: "translate(-50%, -50%)",
              background: "hsl(var(--surface))",
              color: "hsl(var(--ink))",
              padding: 20,
              borderRadius: 0,
              border: "1px solid hsl(var(--rule-strong))",
              width: "min(520px, 90vw)",
              zIndex: 10001,
              fontFamily: "var(--font-body)",
            }}
          >
            <div
              style={{
                fontFamily: "var(--font-display)",
                fontSize: 16,
                fontWeight: 600,
                marginBottom: 14,
              }}
            >
              UI feedback
            </div>

            <div style={eyebrow}>Route</div>
            <div
              style={{
                fontSize: 13,
                fontFamily: "var(--font-mono)",
                marginBottom: 10,
              }}
            >
              {captured.route}
            </div>

            <div style={eyebrow}>Anchor</div>
            <div style={{ fontSize: 13, marginBottom: 10 }}>{captured.anchor}</div>

            <div style={eyebrow}>Selector</div>
            <div
              style={{
                fontSize: 12,
                fontFamily: "var(--font-mono)",
                color: "hsl(var(--ink-3))",
                marginBottom: 14,
                wordBreak: "break-all",
              }}
            >
              {captured.selector}
            </div>

            <textarea
              autoFocus
              placeholder="What's wrong / what would you change?"
              value={comment}
              onChange={(e) => setComment(e.target.value)}
              onKeyDown={(e) => {
                if ((e.metaKey || e.ctrlKey) && e.key === "Enter") save();
              }}
              style={{
                width: "100%",
                minHeight: 100,
                padding: 8,
                border: "1px solid hsl(var(--rule))",
                borderRadius: 0,
                fontSize: 13,
                fontFamily: "var(--font-body)",
                background: "hsl(var(--surface))",
                color: "hsl(var(--ink))",
                resize: "vertical",
                boxSizing: "border-box",
              }}
            />

            <div
              style={{
                display: "flex",
                justifyContent: "space-between",
                alignItems: "center",
                marginTop: 14,
              }}
            >
              <span
                style={{
                  fontSize: 11,
                  fontFamily: "var(--font-mono)",
                  color: "hsl(var(--ink-4))",
                }}
              >
                ⌘+Enter to save
              </span>
              <div style={{ display: "flex", gap: 8 }}>
                <button
                  onClick={cancel}
                  style={{
                    padding: "7px 14px",
                    border: "1px solid hsl(var(--rule))",
                    background: "hsl(var(--surface))",
                    color: "hsl(var(--ink))",
                    borderRadius: 0,
                    cursor: "pointer",
                    fontSize: 13,
                    fontWeight: 500,
                    fontFamily: "var(--font-body)",
                  }}
                >
                  Cancel
                </button>
                <button
                  onClick={save}
                  disabled={!canSave}
                  style={{
                    padding: "7px 14px",
                    border: "1px solid hsl(var(--mark))",
                    background: "hsl(var(--mark))",
                    color: "hsl(var(--mark-fg))",
                    borderRadius: 0,
                    cursor: canSave ? "pointer" : "not-allowed",
                    opacity: canSave ? 1 : 0.5,
                    fontSize: 13,
                    fontWeight: 500,
                    fontFamily: "var(--font-body)",
                  }}
                >
                  {saving ? "Saving…" : "Save"}
                </button>
              </div>
            </div>
          </div>
        </>
      )}

      {toast && (
        <div
          style={{
            position: "fixed",
            bottom: 76,
            right: 16,
            background: "hsl(var(--mark))",
            color: "hsl(var(--mark-fg))",
            padding: "9px 14px",
            borderRadius: 0,
            fontSize: 13,
            fontWeight: 500,
            fontFamily: "var(--font-body)",
            zIndex: 10002,
          }}
        >
          {toast}
        </div>
      )}
    </div>
  );
}
