import { readFileSync, readdirSync } from "node:fs";
import { join, relative, resolve, sep } from "node:path";
import { describe, expect, it } from "vitest";
import { childSessionGroupLabel } from "@/lib/childSessions";

/**
 * ONE place decides how this app names the sessions a session started.
 *
 * Nine lists now show that count, and each of them once had, or could grow, its
 * own wording. The contribute tree really did: it announced `+ 1 child session`
 * from a fold of its own while every other list announced `1 child session`, so
 * the same fact read two ways on two pages of the same app. A viewer cannot be
 * expected to work out that those are the same thing.
 *
 * This is a SOURCE guard rather than a rendering one, because a rendering test
 * can only see the lists it happens to mount: a tenth list added next month
 * with its own words would render correctly on its own terms and no mounted
 * test would notice. Reading the source is what makes the rule hold for the
 * list nobody has written yet.
 */

const SOURCE_ROOT = resolve(process.cwd(), "src");

/** The one module allowed to write the wording. Named rather than counted, so
 *  a second producer fails by naming itself and moving the helper fails by
 *  naming its new home. */
const THE_LABEL_HELPERS = ["src/lib/childSessions.ts"];

/** The wording, as a viewer reads it. */
const WORDING = /child session/i;

/**
 * The same wording inside a string the code builds -- a quoted or backticked
 * literal -- rather than in prose about it.
 *
 * Documentation is allowed to discuss the label; only PRODUCING it is
 * restricted. Without this distinction a comment explaining the rule would
 * break the rule.
 */
const WORDING_IN_A_LITERAL = /['"`][^'"`\n]*child session/i;

/** Every production source file. Tests, test support and fixtures are left
 *  out: they restate the wording on purpose, so that a change to what the code
 *  produces fails against an independent statement of it rather than quietly
 *  agreeing with itself. */
function productionSourceFiles(dir: string, found: string[] = []): string[] {
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const full = join(dir, entry.name);
    if (entry.isDirectory()) {
      if (entry.name === "test" || entry.name === "testdata") continue;
      productionSourceFiles(full, found);
      continue;
    }
    if (!/\.tsx?$/.test(entry.name)) continue;
    if (/\.test\.tsx?$/.test(entry.name)) continue;
    found.push(full);
  }
  return found;
}

function posix(path: string): string {
  return path.split(sep).join("/");
}

describe("the sessions one session started are named in exactly one place", () => {
  const files = productionSourceFiles(SOURCE_ROOT);

  it("reads real production source, so the guard can see a second producer at all", () => {
    // A guard that walked an empty tree would pass forever. This proves the
    // walk reaches the module that DOES produce the wording.
    expect(files.map((file) => posix(relative(process.cwd(), file)))).toEqual(
      expect.arrayContaining(THE_LABEL_HELPERS),
    );
  });

  it("is produced by the one label helper and by nothing else", () => {
    const producers = files
      .filter((file) => {
        const source = readFileSync(file, "utf8");
        if (!WORDING.test(source)) return false;
        return source.split("\n").some((line) => WORDING_IN_A_LITERAL.test(line));
      })
      .map((file) => posix(relative(process.cwd(), file)))
      .sort();

    expect(
      producers,
      "every list in this app must ask childSessionGroupLabel for these words; a second source of them " +
        "is how one page came to say `+ 1 child session` while the rest said `1 child session`",
    ).toEqual([...THE_LABEL_HELPERS].sort());
  });

  it("states a bare count, with no leading mark", () => {
    // Written out rather than derived, so a change to the helper fails here
    // instead of agreeing with itself.
    expect(childSessionGroupLabel(1)).toBe("1 child session");
    expect(childSessionGroupLabel(2)).toBe("2 child sessions");
    expect(childSessionGroupLabel(0)).toBe("0 child sessions");
  });
});
