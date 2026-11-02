# Adding a log rule

There are two ways to add a log rule:

- **In the console** (admins): **Log rules → + New rule**, or draft one with Claude from a new error pattern or a
  learning. It is stored in the console database and takes effect at once. See
  [Log rules](../user-guide/log-rules.md).
- **In the code**: add it to `internal/logrules/rules.yaml`, so it ships as a built-in in every deployment. This page
  covers that.

## Steps

1. Find the message's source in the prophecy repository: the file and line that logs it.

2. Add the rule to `internal/logrules/rules.yaml`:

    ```yaml
    rules:
      - id: jvm-oom                         # kebab-case, unique, stable (findings reference it)
        title: JVM out of memory            # short; shown on pills and in tables
        services: [metagraph, app]          # pod-name prefixes without "-prophecy"; empty = every pod
        pattern: 'java\.lang\.OutOfMemoryError'   # Go RE2, matched anywhere; (?i) for case-insensitive
        severity: error                     # error | warning | info
        meaning: The JVM ran out of heap; the pod is about to be OOM-killed.
        fix: Raise the memory limit, or find the job that spikes heap.
        source: modules/metagraph/src/main/scala/.../GraphIndexer.scala:142
        examples:                           # at least one realistic full line that MUST match
          - '2026-10-08 10:58:13.552 ERROR [akka-7] c.p.m.GraphIndexer - java.lang.OutOfMemoryError: Java heap space'
        not_examples:                       # optional lines that must NOT match
          - '2026-10-08 10:58:13.552 INFO  [main] c.p.m.Server - OutOfMemoryError handler installed'
    ```

3. If the service is new, add a `formats` entry so the parser can read its level word:

    ```yaml
    formats:
      - services: [metagraph]
        example: '[ERROR][2026-10-08_10:58:13.551] P.GiteClient - Gitea is not coming up.'
        level: '^\[(TRACE|DEBUG|INFO|WARN|ERROR)\]\['
    ```

    `level` is a regexp with one capture group for the level word.

4. Run the tests. `TestDefaultRulesAreValid` checks every rule compiles and matches its examples and none of its
   not-examples:

    ```bash
    go test ./internal/logrules/
    ```

5. Build and roll out a new image ([Upgrading](../operations/upgrading.md)).

An admin's override of a built-in rule (same ID) keeps winning over your new default until someone clicks **Reset**.

## Rules of thumb

- A rule should mean something **actionable**. Never a bare `ERROR` or `Exception` rule.
- Anchor on the **stable** part of the message, not on variable IDs, paths or numbers (`\d+`).
- Prefer a `services` list over a very generic pattern.
- RE2 has no look-around and no backreferences.
- The pattern should contain a literal of at least four characters (a word from the message). The parser's prefilter
  uses such literals to skip most lines without running the regexp, and the Loki query only fetches lines containing
  one. A rule without one always runs its regexp, on every line.

The full format is in `internal/logrules/FORMAT.md`.

## Metric rules

Metric rules live in `internal/metricrules/rules.yaml` and are read-only in the UI.

1. Write a PromQL expression that returns **only the series that are a problem**, and keeps the `cluster` label plus
   whatever identifies the entity (`namespace`/`pod`/`container`, `node`, `persistentvolumeclaim`, `deployment`,
   `job`). It runs once per Grafana instance and covers every cluster of that instance.
2. Try it in Grafana's Explore against the Thanos datasource first.
3. Add the rule:

    ```yaml
    - id: container-restarting
      title: Restarting repeatedly
      severity: warning        # error | warning | info
      entity: container        # container | pod | service | deployment | pvc | node | cluster
      value: count             # percent | count | seconds | rate | none
      expr: |
        max by (cluster, namespace, pod, container) (increase(kube_pod_container_status_restarts_total[30m])) > 2
      meaning: The container restarted more than twice in the last 30 minutes.
      fix: Read the previous container's logs for the crash reason.
    ```

4. Run `go test ./internal/metricrules/` and roll out a new image.
