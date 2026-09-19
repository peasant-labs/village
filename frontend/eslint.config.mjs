import { defineConfig, globalIgnores } from "eslint/config";
import nextVitals from "eslint-config-next/core-web-vitals";
import nextTs from "eslint-config-next/typescript";

const eslintConfig = defineConfig([
  ...nextVitals,
  ...nextTs,
  // Override default ignores of eslint-config-next.
  globalIgnores([
    // Default ignores of eslint-config-next:
    ".next/**",
    "out/**",
    "build/**",
    "next-env.d.ts",
  ]),
  {
    // The Playwright journey scripts are not React, but their fixture callbacks
    // follow Playwright's convention of naming the second parameter `use`. The
    // React hooks rule reads `await use(...)` inside `theme` and `context` as a
    // hook call outside a component, which it is not, so the rule does not apply
    // to this directory's scripts rather than being worked around file by file.
    // The extensions are the scripts it runs; a `.jsx`/`.tsx` file here would be
    // React and would keep the rule.
    files: ["scripts/journey/**/*.{js,mjs,cjs,ts,mts,cts}"],
    rules: { "react-hooks/rules-of-hooks": "off" },
  },
]);

export default eslintConfig;
