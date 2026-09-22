---
# Required. One of: proposed | accepted | rejected | withdrawn | deprecated | superseded.
status: proposed

# Optional. Free-form; not parsed by the linter.
date: YYYY-MM-DD

# Optional. Glob patterns (doublestar syntax) that decide which changed
# files this ADR applies to. The advisory checker defaults to ["**/*"]
# if absent; deterministic planning requires an explicit nonempty scope.
# Negation works: prefix a pattern with "!" to exclude.
applies_to:
  - "**/*"

# Optional deterministic review requirements. Register each capability in
# .adrian/review-capabilities.yml first. These name expertise, not agents.
# review:
#   requires: [visual-product-judgment]
#   evidence: [rendered-preview]
#
# To give different parts of one ADR different obligations, replace the
# flat applies_to above with typed scopes (without a top-level review):
# applies_to:
#   - paths: ["src/**/*.tsx", "!src/**/*.test.tsx"]
#     review:
#       requires: [visual-product-judgment]
#   - paths: ["src/**/*.test.tsx"]
#     review:
#       requires: [test-quality]

# Optional. One of: lite | standard | complex. Controls how aggressively
# the linter chunks diffs and how much context it sends to the model.
# Defaults to "standard" if absent.
complexity: standard

# Optional. Cheap substring pre-filter: if none of these strings appear
# in the changed-file diff, the LLM call is skipped entirely (not passed).
# This never removes deterministic review requirements. Use this
# for ADRs that forbid specific library names, function calls, etc.
# Accepts a single string or a list of strings.
pre_filter:
  - "someBannedSymbol"

# Optional. Marks this ADR as enforced by external tooling (linter rule,
# type check, etc.) so the LLM does not re-check it. Free-form string;
# present-vs-absent is what matters.
# enforced_by: "eslint-plugin-foo/no-bar"

# Optional. Set to false to ask the model to evaluate each file in
# isolation without surrounding diff context. Defaults to true.
diff_context: true

# Optional, set automatically by `adrian supersede`. Points at the
# replacement ADR's id.
# superseded_by: "0042"
---

# {{number}}. {{title}}

## Context

Forces at play - technological, social, project constraints.
What problem prompted this? Value-neutral description of the situation.

## Decision

We will [decision statement in active voice].

[If the decision involves code patterns, include concrete examples:]

**Forbidden pattern:**
```typescript
// ❌ BAD - [explanation]
[example of what NOT to do]
```

**Required pattern:**
```typescript
// ✅ GOOD - [explanation]
[example of what TO do]
```

## Consequences

**Positive:**
- [What becomes easier]
- [What becomes more consistent]

**Negative:**
- [What becomes harder]
- [What constraints we accept]

**Neutral:**
- [Other impacts worth noting]

## References

- [Related PRs, issues, or documentation]
