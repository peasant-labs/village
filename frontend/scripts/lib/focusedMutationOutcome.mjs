/**
 * Classify the focused Vitest JSON report one mutation run produced.
 *
 * A mutation claims its corpus edit reaches either a MOUNTED ASSERTION or the
 * FIXTURE LOADER. Those are different failure classes, and only structured
 * results tell them apart: a suite that fails to collect emits no executed test
 * at all, so the fixture loader's own error can name the case (and therefore the
 * test) while no assertion ever ran. Searching raw console output for that name
 * would read such a broken fixture as a killed assertion. Reading the JSON
 * report keeps the two apart, so an `assertion` mutation is only ever satisfied
 * by an executed test that actually failed.
 */

/** One failed assertion's report entry; only the fields this module reads. */
function assertionName(assertion) {
  return `${assertion?.fullName ?? ""} ${assertion?.title ?? ""}`;
}

/**
 * @param {unknown} report parsed JSON report from `vitest --reporter=json`
 * @param {string} testName the focused test the mutation expects to exercise
 * @returns {{ kind: "assertion-failure" | "load-failure" | "passed" | "unknown", detail: string }}
 */
export function classifyFocusedOutcome(report, testName) {
  const suite = report != null && typeof report === "object" ? report : null;
  const results = Array.isArray(suite?.testResults) ? suite.testResults : [];
  const assertions = results.flatMap((file) =>
    Array.isArray(file?.assertionResults) ? file.assertionResults : [],
  );

  const failedAssertion = assertions.find(
    (assertion) => assertion?.status === "failed" && assertionName(assertion).includes(testName),
  );
  if (failedAssertion != null) {
    const [message = ""] = failedAssertion.failureMessages ?? [];
    return { kind: "assertion-failure", detail: String(message) };
  }

  // No executed failure for the focused test. A failed SUITE with no assertions
  // is a load failure: the corpus edit was rejected before any test ran.
  const loadMessages = results
    .filter((file) => file?.status === "failed")
    .map((file) => String(file?.message ?? ""))
    .filter((message) => message.trim() !== "");
  if (loadMessages.length > 0) {
    return { kind: "load-failure", detail: loadMessages.join("\n") };
  }

  if (
    suite != null &&
    Number(suite.numFailedTests ?? 0) === 0 &&
    Number(suite.numFailedTestSuites ?? 0) === 0
  ) {
    return { kind: "passed", detail: "" };
  }

  return {
    kind: "unknown",
    detail: "the focused run reported neither an executed failure nor a suite-load failure",
  };
}
