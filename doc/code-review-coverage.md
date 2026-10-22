# Code-review coverage producer and transport

`adrian code-plan` exports deterministic review inputs. It does not review code,
select a model, dispatch workers or publish a verdict. The existing `adrian plan`
capability-routing schema and fingerprints retain their existing meaning.

```sh
adrian code-plan --repository owner/name --base "$trusted_base" \
  --head "$review_head" --policy "$trusted_policy" > code-plan.json
```

Obtain repository identity and all refs from trusted repository/CI metadata.
The supplied repository name is a binding for the consumer to check; ADRian
cannot infer which remote a caller intends from identical Git objects.
Policy must be an ancestor of the trusted base. Uncommitted changes and the
head branch's policy edits do not govern this review.

The separately versioned `adrian-code-plan-v2` envelope includes:

- The original semantic plan and a complete accepted ADR/scope/path census,
  including missing routing annotations and explicit empty capability lists.
- Scope-local evidence requirements. One scope's additional requirement does
  not leak onto another scope sharing the same capability.
- Baseline source versions: head content for current paths, merge-base content
  for deletions and rename origins. Binary bytes use base64; symlinks and Git
  links are explicit kinds and never followed as ordinary source files.
- Complete applicable ADR text, source object identities, content digests, the
  full diff, policy inventory and normalized registry/manifest digests.
- Shared delivery artifacts and bounded byte-range fragments. Canonical content
  appears once; typed references bind each fragment to the exact source, policy
  or change-diff record. Whole-scope descriptors reference every required part.

The outer fingerprint binds repository, provenance, inventories, content and
all delivery bindings. A fingerprint identifies an inventory; it does not prove
that a reviewer understood the code. Consumers independently retain the expected
producer fingerprint and recheck the current head before publication.

Full-code delivery deliberately does not use lexical prefilters to waive rule
assessment. Each applicable full ADR, affected source versions and per-change
Git diffs remain required. The baseline also retains the global cross-file diff.
A 4096 estimated-token materialized-fragment budget includes its JSON metadata;
256 fragments are admitted. These byte-derived estimates are not provider usage.
Capacity overflow retains the entire inventory and reports technical
incompleteness. Empty artifacts retain a zero-length fragment and their metadata.

Fragments are transport units, not model tasks or independent rule judgments.
The consumer must reconstruct all parts, verify byte hashes/ranges, count actual
fetched context and prompts, and assess each complete scope across its fragments.
Deduplicating pointers does not make their context free. Native materialization
requires an independently retained expected shared-plan fingerprint. Runtime
and specialized source kinds still require the appropriate evidence/assessment.

`--legacy-packets` explicitly exports the older `adrian-code-plan-v1` envelope
and inline `review-packet-v1` splitter. Its per-policy Cartesian repetition,
policy-budget skips and capacity overflow remain visible fallback work. This
compatibility mode does not replace shared full-code coverage or relax checks.
Its ambiguous Git headers retain trusted scope paths rather than guessed names.

Producer reads ignore Git repository-location environment overrides and object
replacement, disable lazy fetching, normalize to the repository root, and request
non-relative diffs. Each Git subprocess has a 30-second deadline; each of the two
producer stages has a two-minute deadline. Reads and policy aggregation have
8MiB limits, and the final encoded envelope must fit 8MiB. Failure emits no plan;
a consumer must report technical incompleteness, never an author code defect.
Prepare required Git objects separately before running the read-only producer.

## Opaque external adapter transport

`execute-plan` restores the mechanical packet-executor seam from the older
adr-lint implementation, reconciled with the current module. It is independent
of `check` and adds no model backend or credentials.

```sh
adrian execute-plan --plan native-packets.json --adapter /absolute/adapter \
  --expected-plan-sha256 "$trusted_native_plan_hash" \
  --record /private/new-attempt.json --timeout 60s
```

The public command requires an independently retained expected hash and a new
record path. It refuses to overwrite a prior attempt. The adapter receives one
packet JSON value on stdin and returns opaque JSON on stdout. It runs in order,
with no shell interpolation or parallel scheduling. The collector owns adapter
identity, assignment authentication, execution isolation and semantic validation.

The record is atomically saved before execution, after each successful receipt,
and after completion/failure. Failure returns nonzero and emits an incomplete
run containing retained receipts and the failed packet index. A fresh attempt
can use those receipts only after the consumer verifies matching producer,
packet, dependency and collector identities. Automatic resume is not provided.

Default total execution time is 60 seconds, configurable up to 30 minutes.
Cancellation kills the adapter process group on supported macOS/Linux targets.
Stdout is bounded to 1MiB per result, stderr to 64KiB, and aggregate result bytes
to 8MiB. Output overflow, timeout, malformed JSON, persistence errors or stale
identity are technical failures. Cancellation is checked before each invocation.

Valid JSON such as `null` is intentionally opaque transport data, not a valid
semantic review receipt. A completed transport run with skipped/fallback scopes
is not complete review coverage. The consuming workflow must reject missing,
stale, foreign or invalid assessments, require actual runtime evidence where
needed, consolidate findings across chunks, preserve late genuine discoveries,
and use its existing quorum/CAS/lease authority for publication.

This producer and transport do not activate a live review lane or satisfy a
historical canary hold. Rollout requires separate measured shadow validation.
