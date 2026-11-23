import { defineConfig, globalIgnores } from "eslint/config";
import nextVitals from "eslint-config-next/core-web-vitals";
import nextTs from "eslint-config-next/typescript";
import tseslint from "typescript-eslint";
import unstring from "eslint-plugin-unstring";

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
    files: ["src/**/*.{ts,tsx}"],
    ignores: ["src/**/*.test.{ts,tsx}", "src/**/*.spec.{ts,tsx}", "src/**/*.stories.{ts,tsx}", "src/**/*.type-test.{ts,tsx}", "src/**/testdata/**", "src/test/**"],
    languageOptions: {
      parser: tseslint.parser,
      parserOptions: { project: "./tsconfig.eslint.json", tsconfigRootDir: import.meta.dirname },
    },
    plugins: { unstring },
    rules: { "unstring/closed-set-literal": "error" },
  },
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
