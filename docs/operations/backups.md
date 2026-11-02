# Backups

All of the console's state is on one persistent volume, mounted at `/data`:

- the SQLite database (`/data/ai-console.db`, plus its `-wal` and `-shm` files in WAL mode): sessions, sealed Claude
  keys, chats, investigations, learnings, findings, rule changes, Grafana settings, costs, the audit log;
- `DATA_DIR` files: log parser hits and state, metric rule state and events, the certificate inventory, the price
  cache. See [Data model](../concepts/data-model.md#files-under-data_dir).

Back it up with **GCE persistent disk snapshots**. The volume is a GCE persistent disk provisioned by the
`pd.csi.storage.gke.io` driver.

!!! note
    A backup is only useful with the same `ENCRYPTION_KEY`. Without it, sealed sessions, Claude keys and Grafana tokens
    in the database cannot be decrypted. Make sure the key is stored safely outside the cluster.

## Find the disk

```bash
PV=$(kubectl -n ai-console get pvc ai-console-data -o jsonpath='{.spec.volumeName}')
kubectl get pv "$PV" -o jsonpath='{.spec.csi.volumeHandle}{"\n"}'
# projects/<PROJECT>/zones/<ZONE>/disks/<DISK>
```

## Take a snapshot

=== "Consistent (short outage)"

    Stop the console so SQLite is not writing, snapshot, then start it again:

    ```bash
    kubectl -n ai-console scale deploy/ai-console --replicas=0
    kubectl -n ai-console wait --for=delete pod -l app=ai-console --timeout=120s

    gcloud compute disks snapshot <DISK> \
      --project <PROJECT> --zone <ZONE> \
      --snapshot-names ai-console-data-$(date +%Y%m%d-%H%M)

    kubectl -n ai-console scale deploy/ai-console --replicas=1
    ```

=== "Online (crash-consistent)"

    Snapshot while the console runs. SQLite in WAL mode recovers from a crash-consistent copy like from a power cut:
    committed transactions are kept.

    ```bash
    gcloud compute disks snapshot <DISK> \
      --project <PROJECT> --zone <ZONE> \
      --snapshot-names ai-console-data-$(date +%Y%m%d-%H%M)
    ```

Take a consistent snapshot before risky operations, such as [moving the volume](moving-volume.md) or an upgrade you
may need to roll back. To snapshot on a schedule, attach a snapshot schedule (resource policy) to the disk with
`gcloud compute resource-policies create snapshot-schedule` and `gcloud compute disks add-resource-policies`.

List snapshots:

```bash
gcloud compute snapshots list --project <PROJECT> --filter="name~^ai-console-data"
```

## Restore from a snapshot

1. Stop the console:

    ```bash
    kubectl -n ai-console scale deploy/ai-console --replicas=0
    ```

2. Create a disk from the snapshot in the same zone as the cluster's nodes:

    ```bash
    gcloud compute disks create ai-console-data-restored \
      --project <PROJECT> --zone <ZONE> \
      --source-snapshot <SNAPSHOT> --type pd-balanced
    ```

3. Create a PersistentVolume for it and point a new claim at it, as in
   [Moving the data volume](moving-volume.md#use-an-existing-disk) (use the restored disk's handle).

4. Point the Deployment at the new claim (or replace `ai-console-data`), and scale back up:

    ```bash
    kubectl -n ai-console scale deploy/ai-console --replicas=1
    ```

## Copy the database file

To inspect data offline, copy the database out of the running pod. The image has no shell, so `kubectl cp` (which needs
`tar`) does not work against it. Instead, take a snapshot and attach a restored disk to a debug pod, or read the files
from a temporary pod that mounts the claim while the console is scaled to zero:

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: ai-console-data-reader
  namespace: ai-console
spec:
  securityContext:
    runAsUser: 65532
    runAsGroup: 65532
    fsGroup: 65532
  containers:
    - name: reader
      image: busybox:1.36
      command: ["sleep", "3600"]
      volumeMounts:
        - name: data
          mountPath: /data
  volumes:
    - name: data
      persistentVolumeClaim:
        claimName: ai-console-data
```

```bash
kubectl -n ai-console cp ai-console-data-reader:/data/ai-console.db ./ai-console.db
kubectl -n ai-console delete pod ai-console-data-reader
```

Copy `ai-console.db-wal` (and `-shm`) too if they exist, into the same directory: SQLite applies the WAL when it next
opens the database.

!!! warning
    The database holds sealed tokens and keys. Treat a copy like a secret and delete it when you are done.
