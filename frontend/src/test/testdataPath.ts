import { resolve } from "node:path";

// unstring-allow: data-path-literal — the one place a test names the fixture directory; every reader resolves through testdataPath
const TESTDATA_DIR = resolve(process.cwd(), "src/testdata");

/** The absolute path of one file under `src/testdata`, named relative to that directory. */
export function testdataPath(name: string): string {
  return resolve(TESTDATA_DIR, name);
}
