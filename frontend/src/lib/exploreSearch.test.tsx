import { useRef } from "react";
import { act, render } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { openExploreSearch, useExploreSearchFocus } from "@/lib/exploreSearch";

// The search's box takes the cursor on arrival even though the sidebar item
// that asked for it is still on screen with the focus, and leaves it alone
// once the person has put the cursor somewhere else.

function Explore() {
  const root = useRef<HTMLDivElement>(null);
  useExploreSearchFocus(root);
  return (
    <div ref={root}>
      <input type="text" aria-label="search transcripts" />
    </div>
  );
}

afterEach(() => {
  vi.useRealTimers();
  document.body.innerHTML = "";
});

describe("the explore search box on arrival", () => {
  it("takes the cursor from the sidebar item that asked for it", async () => {
    const trigger = document.createElement("button");
    document.body.append(trigger);
    trigger.focus();
    const push = vi.fn();
    openExploreSearch(push, "/collectives");
    expect(push).toHaveBeenCalledWith("/discover?view=transcripts");
    const { getByRole } = render(<Explore />);
    expect(document.activeElement).toBe(getByRole("textbox"));
  });

  it("leaves the cursor where the person put it", async () => {
    const elsewhere = document.createElement("input");
    document.body.append(elsewhere);
    openExploreSearch(vi.fn(), "/collectives");
    elsewhere.focus();
    render(<Explore />);
    expect(document.activeElement).toBe(elsewhere);
  });

  it("asks nothing once the request is stale", async () => {
    openExploreSearch(vi.fn(), "/collectives");
    vi.useFakeTimers({ toFake: ["Date"] });
    vi.setSystemTime(Date.now() + 60_000);
    const { getByRole } = render(<Explore />);
    expect(document.activeElement).not.toBe(getByRole("textbox"));
  });

  it("takes the cursor at once when discover is already open", async () => {
    vi.useFakeTimers({ toFake: ["Date"] });
    vi.setSystemTime(Date.now() + 120_000);
    const push = vi.fn();
    const { getByRole } = render(<Explore />);
    act(() => openExploreSearch(push, "/discover"));
    expect(push).not.toHaveBeenCalled();
    expect(document.activeElement).toBe(getByRole("textbox"));
  });
});
