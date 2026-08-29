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
 * The source with every comment taken out, leaving only what the file PUTS in
 * front of a viewer.
 *
 * Documentation is allowed to discuss the label; only producing it is
 * restricted, and without that distinction a comment explaining this rule would
 * break the rule. The distinction cannot be drawn by looking for a quote beside
 * the wording, which is what this guard did first: a file can spell the words as
 * JSX TEXT -- `<span>{count} child sessions</span>` -- and carry no quote on
 * that line at all. That is precisely the shape the offender this guard was
 * written for had, so the quote form caught it only by accident. Reading the
 * code with its comments removed asks the question the rule actually means.
 *
 * Strings are KEPT, quotes and all: a string is something the file produces.
 * Only line and block comment spans are dropped, and a line-comment mark inside
 * a string is not one of them.
 */
function codeWithoutComments(source: string): string {
  let code = "";
  let quote: string | null = null;
  let inLineComment = false;
  let inBlockComment = false;

  for (let i = 0; i < source.length; i += 1) {
    const character = source[i];
    const next = source[i + 1];

    if (inLineComment) {
      if (character === "\n") {
        inLineComment = false;
        code += character;
      }
      continue;
    }
    if (inBlockComment) {
      if (character === "*" && next === "/") {
        inBlockComment = false;
        i += 1;
      } else if (character === "\n") {
        // Newlines are kept so a failure can still be read line by line.
        code += character;
      }
      continue;
    }
    if (quote !== null) {
      code += character;
      if (character === "\\" && next !== undefined) {
        code += next;
        i += 1;
        continue;
      }
      if (character === quote) quote = null;
      continue;
    }
    if (character === "/" && next === "/") {
      inLineComment = true;
      i += 1;
      continue;
    }
    if (character === "/" && next === "*") {
      inBlockComment = true;
      i += 1;
      continue;
    }
    if (character === '"' || character === "'" || character === "`") quote = character;
    code += character;
  }
  return code;
}

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

  it("tells the code apart from the prose about it, so the rule can be documented", () => {
    // Read off the one module that both PRODUCES the wording and writes at
    // length about it, so this holds against real source rather than a sample
    // written to suit it.
    const stripped = codeWithoutComments(
      readFileSync(resolve(SOURCE_ROOT, "lib", "childSessions.ts"), "utf8"),
    );
    expect(
      stripped,
      "a sentence that exists only in that module's documentation is not code",
    ).not.toContain("NOTHING IS EVER REMOVED FROM THE LIST HERE");
    expect(stripped, "the code the documentation describes is still there to read").toContain(
      "export function childSessionGroupLabel",
    );
    expect(
      WORDING.test(stripped),
      "and the wording it produces survives, or the guard below would see no producer at all",
    ).toBe(true);
  });

  it("is produced by the one label helper and by nothing else", () => {
    const producers = files
      .filter((file) => WORDING.test(codeWithoutComments(readFileSync(file, "utf8"))))
      .map((file) => posix(relative(process.cwd(), file)))
      .sort();

    expect(
      producers,
      "every list in this app must ask childSessionGroupLabel for these words, whether it would spell them in a " +
        "string or straight into the markup; a second source of them is how one page came to say " +
        "`+ 1 child session` while the rest said `1 child session`",
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
