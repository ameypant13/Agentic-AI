# Copilot evaluation

Ten recorded cases, each a question plus the tool outputs a real investigation would see. `make eval` runs the
Copilot on them against Claude with those outputs replayed (nothing reaches control-hub, clusters or Grafana) and
writes `evals/report.md`: pass/fail per case, tool calls, calls with no recorded data, tokens.

```bash
ANTHROPIC_API_KEY=... make eval                       # all cases
ANTHROPIC_API_KEY=... make eval EVAL_ARGS="-only pg16-public-schema,healthy-tenant"
```

Every run costs tokens (about 10 Opus conversations). Run it when the prompt, a tool or the model changes, and
compare reports before shipping the change.

## A case

```yaml
id: short-id
title: one line
question: what the user asks
learnings: []            # optional approved learnings present during the run
fixtures:                # first match wins; unmatched calls return "no data recorded"
  - tool: get_job
    match: {job_id: job-1}   # substrings of input fields, case-insensitive; omit to match any call
    output: {...}            # or a string, or `error: message`
expect:
  categories: [networking-dns]       # accepted investigation categories
  rootCause: [[subnet], [tag, kubernetes.io/role/elb]]   # every group needs one term
  mustNot: [certificate expired]     # known wrong answers
  noInvestigation: false             # true when nothing is wrong
  maxToolCalls: 18
```

Add a case for every real investigation the Copilot got wrong, or one worth keeping right. The report lists the
unrecorded calls of failed cases: add fixtures for the ones the right diagnosis depends on.
