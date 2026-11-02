# Weekly digest

The weekly digest summarises the last 7 days across the fleet, from the console's own data. **No AI** is involved. It
is posted to Slack once a week, previewed on the [Weekly digest](../user-guide/digest.md) page, and can be sent on
demand by admins.

## Contents

The digest has a headline ("Week of &lt;date&gt;: …") and four sections, each with a link into the console:

| Section | Link | What it says |
|---|---|---|
| Findings | `/issues` | Counts of new critical findings, new warnings and findings resolved; up to five new critical findings; critical findings open for over a week. |
| Certificates | `/certificates` | Certificates expiring within 21 days (up to 8 listed), or "Nothing expires in the next 21 days." |
| Cost (only with `COST_ENABLED`) | `/costs` | Compute cost this week against the week before, the three biggest [cost changes](cost-changes.md), right-sizing potential a month and the containers that need more. |
| Investigations and learning | `/investigations` | The number of investigations recorded (up to five listed), draft learnings waiting for review, and new error patterns no log rule matches. |

Links are built from `PUBLIC_URL` (default `https://controlhub.app.prophecy.ai`).

The digest is **admins only**, in the API and the UI, because it names every investigation; non-admins only see their
own.

## Schedule

| Variable | Default | Meaning |
|---|---|---|
| `DIGEST_ENABLED` | `true` | Turn the weekly post on or off. |
| `DIGEST_WEEKDAY` | `1` | Day of the week, 0 = Sunday. |
| `DIGEST_HOUR_UTC` | `4` | Hour of the day in UTC (04:00 UTC is 09:30 IST). |

A scheduler checks every 10 minutes. During the configured weekday and hour it posts the digest unless it was sent less
than 24 hours ago. The last send time is stored in the database, so a restart never sends it twice.

Posting goes through the same Slack webhook as the monitors (`SLACK_WEBHOOK_URL`). With `SLACK_MUTE=true` or no
webhook, nothing is posted: a manual **Send to Slack now** returns `sent: false` with the reason.
