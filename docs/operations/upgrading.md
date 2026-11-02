# Upgrading and redeploying

The console has no separate migration step: at start-up it runs gorm `AutoMigrate` on every table, which adds new
tables and columns. Background state on the volume is picked up where it left off.

## Roll out a new build

1. Make sure the change builds and passes tests:

    ```bash
    cd ai-console
    make build test
    (cd web && npm run build)
    ```

2. Build and push the image (this also copies the current knowledge graph into it):

    ```bash
    make push TAG=<tag>
    ```

3. Roll it out.

    === "Same tag"

        With `imagePullPolicy: Always`, restarting pulls the new image:

        ```bash
        kubectl -n ai-console rollout restart deploy/ai-console
        kubectl -n ai-console rollout status deploy/ai-console
        ```

    === "New tag"

        ```bash
        kubectl -n ai-console set image deploy/ai-console \
          ai-console=gcr.io/prophecy-cloud/control-hub-ai-console:<tag>
        kubectl -n ai-console rollout status deploy/ai-console
        ```

4. Check the start-up log and the UI as in [Installing → Verify](install.md#9-verify).

Because the Deployment uses `Recreate`, the old pod stops before the new one starts: expect a short outage. Copilot
replies streaming at that moment are cut off; users resend their question.

!!! warning "Shared images"
    If other people test builds under the same tag, a restart rolls out whatever was pushed last. Check which image the
    pod runs (`kubectl -n ai-console get pod -l app=ai-console -o jsonpath='{.items[0].status.containerStatuses[0].imageID}'`)
    before and after.

## Change configuration

- **Plain environment variables** (in the Deployment): edit them with `kubectl -n ai-console edit deploy/ai-console`
  or `kubectl set env`; this restarts the pod.
- **Secrets** (in `ai-console-secrets`): update the secret, then restart, because environment variables are read
  only at start-up:

    ```bash
    kubectl -n ai-console rollout restart deploy/ai-console
    ```

- **`CONSOLE_ADMIN_EMAILS`** is read at start-up but checked on every request, so a restart makes changes take effect
  without anyone signing in again.
- **Grafana instances and mappings** can be changed in Settings without a restart.

## Update the knowledge graph

The Copilot's knowledge graph is baked into the image. After the control-hub `.knowledgegraph/` changes, rebuild and
roll out (`make push` runs `make kg` first).

## Renew the console's TLS certificate

The ingress uses a copy of the wildcard secret, which does not renew with the original. When the wildcard is renewed,
copy it again ([Installing → step 5](install.md#5-copy-the-tls-secret)). nginx picks up the updated secret without a
restart.

## Roll back

Point the Deployment back at the previous image tag. New columns or tables added by the newer version are ignored by
the older one. Take a [snapshot](backups.md) before upgrades you are unsure about.
