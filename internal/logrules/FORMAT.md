# Log rule file format (`rules.yaml`)

The log parser matches each log line against these rules. No AI is involved; the Copilot only
explains hits. Rules ship embedded in the image (`rules.yaml`, the defaults) and admins can add,
edit or disable rules from the Log rules page (stored in the console's database, layered on top).

```yaml
version: 1

# How each service prints a line, so the UI can pull out level and timestamp.
# `level` is a regexp with one capture group for the level word.
formats:
  - services: [metagraph, app, execution]
    example: '2026-10-08 10:58:13.551 ERROR [akka-7] c.p.m.GraphIndexer - Failed to index project 4812'
    level: '^\S+ \S+ (TRACE|DEBUG|INFO|WARN|ERROR)\b'

rules:
  - id: jvm-oom                         # kebab-case, unique, stable (findings reference it)
    title: JVM out of memory            # short; shown on pills and in tables
    services: [metagraph, app]          # pod-name prefixes without the "-prophecy" suffix
                                        # (e.g. "metagraph" matches metagraph-prophecy-7d9f…);
                                        # empty or omitted = every pod
    pattern: 'java\.lang\.OutOfMemoryError'   # Go RE2 regexp, matched anywhere in the line;
                                        # case-sensitive unless it starts with (?i)
    severity: error                     # error | warning | info
    meaning: The JVM ran out of heap; the pod is about to be OOM-killed.   # one sentence
    fix: Raise the memory limit, or find the job that spikes heap.         # one sentence
    source: modules/metagraph/src/main/scala/.../GraphIndexer.scala:142    # where it is logged ("" for generic rules)
    examples:                           # at least one realistic full line that MUST match
      - '2026-10-08 10:58:13.552 ERROR [akka-7] c.p.m.GraphIndexer - java.lang.OutOfMemoryError: Java heap space'
    not_examples:                       # optional: lines that must NOT match (guards against noise)
      - '2026-10-08 10:58:13.552 INFO  [main] c.p.m.Server - OutOfMemoryError handler installed'
```

Rules of thumb:
- A rule should mean something actionable. Never a bare `ERROR` or `Exception` rule.
- Anchor on the stable part of the message, not on variable IDs, paths, or numbers (`\d+`).
- Prefer the service list over a very generic pattern.
- RE2 has no look-around and no backreferences.
