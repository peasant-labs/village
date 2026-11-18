# Segmented read operational checks

## Authorization and parity

Before any production content is read, an operator must record an explicit
authorization covering the subjects, purpose, time window and report custody.
The authorization reference passed to a check is not itself authorization.
The census reads database shape facts without reading object bodies. The
bounded sample is intended to record its selected subjects before reading
content and to retain no content in its report.

The check computes the whole-document response without invoking read repair.
Run it only within the recorded authorization and the sample's byte and time
bounds. Do not treat a skipped subject or an incomplete report as a clean
parity gate.

## Synthetic ingress and telemetry verification

Before activation, use a disposable test deployment and synthetic accounts,
transcript IDs, entry references and representation values. Record the tested
deployment and configuration, the operator, time, and the inspected logging
and tracing destinations. Send both matching and unmatched routes, including a
deep-link query and an intentionally invalid request. Exercise both successful
and refused reads.

Inspect application, ingress, reverse proxy, CDN, access log, error log,
metric-label and trace/span outputs, including sampled and error paths. Confirm
that none contains the synthetic ID, entry reference, representation, query,
body or credential; a route template and bounded status are acceptable. Check
referrer handling and any redirect or sign-in continuation separately. Retain
only the verification result and configuration evidence, not captured
credentials or request bodies. Any missing destination, uninspectable sampling
path or exposed marker fails the gate; correct the responsible configuration
and repeat the entire check before activation.
