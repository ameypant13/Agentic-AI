# Log rules and error patterns

**Route:** `/log-rules` · "Known messages our services print. The parser matches them in milliseconds; the Copilot only
explains what they mean."

The Log rules page has three tabs:

| Tab | Route | Who |
|---|---|---|
| Log rules | `/log-rules` | Everyone (editing: admins) |
| New error patterns | `/log-rules?view=patterns` | Admins only |
| Metric rules | `/log-rules?view=metrics` | Everyone, read-only; see [Metric rules](metric-rules.md) |

How the parser works is in [Log rules and error patterns](../concepts/log-rules.md).

## Log rules

### Test a log line

Paste a line into **Test a log line** (for example `java.lang.OutOfMemoryError: Java heap space`). Matching rules
appear as you type, with their severity, ID, title and meaning. Add a **Pod name** to only try the rules that apply to
that pod. "No rule matches. Unknown errors are what the Copilot is for."

### The rule table

| Column | Shows |
|---|---|
| Rule | ID and title |
| Services | Pod-name prefixes the rule applies to, or "all pods" |
| Pattern | The Go RE2 regular expression |
| Severity | Error, Warning or Info |
| Meaning · fix | One sentence each |
| Hits 24 h | Matches recorded by scans in the last 24 hours |
| State | Built-in, Custom or Edited, and Disabled |

Filter by text, severity and service, and show disabled rules. Click a row to see its examples, not-examples and
source (where the prophecy repository logs the message). A link with `?rule=<id>` opens and scrolls to a rule.

### Add a rule (admins)

1. Click **+ New rule**.
2. Fill in:
    - **ID**: kebab-case, e.g. `jvm-oom`. IDs cannot be changed later.
    - **Severity**, **Title**.
    - **Services**: comma-separated pod-name prefixes; empty matches every pod.
    - **Pattern**: a Go RE2 regular expression. Anchor on the stable text of the message, never on IDs, numbers or
      paths.
    - **Meaning**, **Fix**: one sentence each.
    - **Examples**: one per line; each must match.
    - **Not examples**: one per line; none may match.
3. Check the live preview. It uses the browser's JavaScript regex as a hint only; the server validates with Go RE2.
4. Click **Create rule**. If the server rejects it (the pattern does not compile, an example does not match, the ID
   exists), the form shows why.

### Edit, disable, reset or delete (admins)

| Button | Effect |
|---|---|
| Edit | Change a rule. Editing a built-in stores an override; the rule is marked **Edited**. |
| Disable / Enable | Stop or resume matching the rule. |
| Reset | On an edited or disabled built-in: discard your edits and the disabled state and return to the default. |
| Delete | On a custom rule: remove it. This cannot be undone. |

## New error patterns (admins)

"Error messages no log rule matches yet, grouped into templates as the parser reads logs. Turn the actionable ones into
rules so the monitors catch them without AI; ignore the noise."

Each row shows the service, a **new today** pill, "N in 24 h · N in 6 h · N total", the cluster or cluster count,
when it was first seen, and the template (variable parts shown as `<*>`). **Show an example line** reveals a real
(masked) line.

Status chips: **To review**, **Turned into rules**, **Ignored**. Filter by service or text.

Only errors from `prophecy*` namespaces and `LOG_SCAN_NAMESPACES` are mined, and test runners in
`PATTERN_IGNORE_SERVICES` (default `sla-tests,e2e-tests`) are left out. See
[Error patterns](../concepts/log-rules.md#error-patterns-the-drain-miner).

### Turn a pattern into a rule

1. Find the pattern in **To review**.
2. Click **Draft rule**. Claude drafts a rule from the template and the real lines, and the console checks that the
   pattern compiles, matches the examples (and the real line) and none of the not-examples.
3. If Claude thinks the message is not specific or actionable enough, the page says "No rule proposed: …". Consider
   **Ignore** instead.
4. Otherwise the rule form opens with the draft: "Claude drafted it and the console checked … Review it, then save;
   nothing is stored until you do."
5. Review and adjust it, then click **Create rule**. The pattern moves to **Turned into rules** with a link to the rule.

### Ignore noise

Click **Ignore** on a pattern that is not worth a rule. **Restore** brings it back. Patterns nobody acts on are dropped
14 days after they stop appearing.
