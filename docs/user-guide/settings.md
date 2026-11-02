# Settings

**Route:** `/settings` (Claude API key) and `/settings?tab=grafana` (Grafana)

## Claude API key

Every user can manage their own key here. "Copilot chats use your personal Claude API key when you set one; otherwise
they use the shared team key. The key is checked with the Claude API before it is stored and is never shown again."

The card shows:

| Field | Meaning |
|---|---|
| Personal key | Configured or Not set |
| Ends in | The last four characters of your key |
| Next chat uses | Your key, Shared team key, or No key |

### Add your key

1. Create an API key at console.anthropic.com. Claude.ai Pro/Max subscriptions cannot be used here.
2. Open **Settings**.
3. Paste the key (it starts with `sk-ant-`) into **Your API key**.
4. Click **Save**.
5. The console checks the key with the Claude API. On success it says "Key saved. New chats will use it." and
   **Next chat uses** shows "Your key".

If the key is rejected you see "that doesn't look like a Claude API key (they start with sk-ant-)" or "the Claude API
rejected this key".

### Replace or remove your key

- To replace it, paste a new key into **Replace your key** and click **Save**.
- To remove it, click **Remove**. New chats then use the shared team key, if there is one.

Keys are encrypted at rest and only their last four characters are kept in clear. Your key pays for your chats, for
log rule drafts you request, and for right-sizing reviews you submit (admins).

## Grafana

The Grafana tab shows which Grafana instances the console reads and how control-hub clusters map to them. Metric
issues, charts, Loki-backed log parsing, cost collection and the Copilot's Grafana tools all depend on it.

### Grafana instances

"Read with a Viewer token. The metrics parser evaluates its rules on every instance each minute."

| Column | Shows |
|---|---|
| Name, URL | The instance |
| Source | `secret` (from `GRAFANA_<NAME>_URL` / `_TOKEN` in the deployment) or `settings` (added here) |
| Clusters | The metrics clusters (the `cluster` label) the instance reports |
| Log clusters | How many Loki clusters (`k8s_cluster_name`) it reports |
| Checked, Evaluated | When the cluster list was last read, and when the metric rules last ran |
| Errors | Connection errors and rule errors |

**Refresh** (everyone) reloads every instance's cluster names now.

#### Add an instance (admins)

1. In that Grafana, create a service account with the **Viewer** role (Administration → Users and access → Service
   accounts) and add a token.
2. In the console, click **+ Add instance**.
3. Enter a **Name** (for example `azure`), the **URL** (must start with `https://`) and the **Service-account token**.
4. Click **Add instance**. The token is checked against Grafana (it must have a Prometheus datasource), stored
   encrypted and never shown again.

**Delete** removes an instance added here. Clusters mapped to it lose their metric issues and charts. Instances from
the deployment secret show "from secret" and can only be changed in the secret.

### Cluster mapping

"Each control-hub cluster is matched to an instance's metrics `cluster` and Loki `k8s_cluster_name` labels
automatically. Admins can override a match."

| Column | Shows |
|---|---|
| Control-hub cluster | The cluster name in control-hub |
| Instance | The Grafana instance |
| Metrics cluster | The `cluster` label |
| Logs cluster | The Loki `k8s_cluster_name` label |
| Mapping | not mapped, auto or override |

Names usually match; known variations (such as an `-aks` suffix) are tried automatically.

#### Override a mapping (admins)

1. Click **Edit** on the cluster. (It is disabled until there is at least one instance.)
2. Pick the **Instance** and the **Metrics cluster**; optionally enter the **Logs cluster**.
3. Click **Save override**.

**Reset to automatic** removes the override. Overrides saved here take precedence over `GRAFANA_MAPPINGS` in the
deployment.
