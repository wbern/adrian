---
status: accepted
applies_to:
  - "go/**/*.go"
pre_filter:
  - "gemini"
  - "vertex"
  - "openai"
---

# 1. Claude is the only LLM provider

## Context

The original TypeScript implementation supported Google Gemini and Vertex
AI alongside Claude. The Go port removes both. Supporting more than one
provider doubles the surface area of the prompt, schema, and result code
paths, and historically caused inconsistent behavior across backends.

## Decision

We will use the Claude Code CLI as the only model-backed code-checking backend. New code
must not introduce Gemini, Vertex AI, OpenAI, or any other LLM provider.
The `--provider` flag accepts only `claude`.

Deterministic ADR validation, lifecycle commands, and review planning do not
invoke a model and need no Claude credentials. Review planning may describe
semantic capabilities for an external consumer to execute; it does not add
another analysis backend to the checker.

## Consequences

**Positive:**
- Single prompt + schema to maintain.
- No API-key management — analysis runs through the user's existing
  Claude Code subscription.

**Negative:**
- Users without a Claude Code subscription cannot run the tool.

**Neutral:**
- The `--provider` flag remains as scaffolding in case future providers
  are added deliberately.
