# Path upload lifecycle

Network path destinations, including mounted SMB and NFS shares, publish an archive and its checksum sidecar through the same staged files. User xattrs and hardlinks are used when the filesystem accepts them. A share that returns `ENOTSUP` or `EOPNOTSUPP` still has to keep ownership. Recovery deletes only a recorded inode at a recorded name. It does not sweep a name prefix and it does not treat a missing source name as a finished delete.

The receipt row is the durability point for every identity below. `destination-receipts.json` is replaced atomically. A stage is owned only after that write returns. The publishing process holds `LOCK_EX` on the inode from the moment it is journaled until rename or process death. Recovery takes `LOCK_EX|LOCK_NB`. A busy lock means the publisher is still active: the receipt stays, and the worker is not called aborted. A filesystem call that outruns `destinationIOTimeout` stays registered (`remoteBusy`). A later or restarted reconciliation does not treat that worker as finished and does not call it aborted.

`Put` stalls used by the crash tests are `journal`, `copy`, `clean-create`, `clean-copy`, `clean-rename`, `rename`, `quarantine`, `quarantine-held`, and `dispose`. Directory sync steps are `link`, `file`, and `delete`.

## Objects

| Object | Names | States | Durability point | Crash recovery | Timeout and late worker | Foreign replacement |
| --- | --- | --- | --- | --- | --- | --- |
| Archive source temp | `.togavsu-<32 hex>.tmp` | created, token stored, journaled, payload, synced, renamed to the final name (xattr) or kept until the clean copy is published (no xattr) | Receipt records name, inode, token, and anchor after the token is fsynced and the anchor directory entry is synced, and before any archive payload byte | Open the recorded name, anchor, or quarantine name. Token and inode must match. Lock must be free. Then quarantine and unlink that inode | Busy lock or `remoteBusy` on the final name, the temp, or the clean file: leave the row. A timed-out cleanup stays tracked and does not clear the row | Inode or token mismatch is not this inode. Quarantine it, restore it with `renameat2(RENAME_NOREPLACE)`, and never replace a name that appeared in the meantime. A parked file stays under the recorded hold name |
| No-xattr publication copy | a second `.togavsu-<32 hex>.tmp` | created empty, journaled, payload, synced, renamed to the final name | Receipt records `clean` and `clean_inode` before the first payload byte. The descriptor stays open and locked through copy, sync, and rename | Same inode check without a token, because a token prefix would be copied into the archive. Lock must be free | Same busy and `remoteBusy` rule. The clean file is a separate key from the source temp | A different inode at that name is restored or parked. It is not deleted. The final object is the renamed clean inode, never the prefixed source |
| Checksum sidecar temp | same temp, anchor, clean, and quarantine names, on the `{id}.sha256` receipt | the archive source row and the sidecar row each run this table | `copyOne` and `ensureValidSidecar` both install the path callback before `Put`. The sidecar row records its own temp before sidecar bytes | Reconcile the sidecar row through `Store` reconciliation. A crash does not leave a pending or uncertain sidecar with no temp identity | A sidecar worker that is busy or late is not aborted and does not release the archive row's remaining identities | The sidecar prefix is stripped the same way. The published `.sha256` has no `TGVTMP1` prefix |
| Private anchor | `.togavsu-<32 hex>.own` | linked after the token sync, journaled with the source, disposed only after its own outcome is recorded | The source journal includes the anchor name when `linkat` succeeded. Empty when the share rejects hardlinks | `openOwned` tries the anchor when the temp name is missing or is a different inode | The anchor is the same inode, so the source lock is the anchor lock | A foreign temp name does not authorize unlinking the anchor. The anchor stays until a later pass has the conflict on the receipt and can lock this inode |
| Quarantine rename | `.togavsu-<32 hex>.qrt` for a temp or clean file, `.oqr` for an anchor | name chosen, journaled, renamed aside, then either unlinked (owned inode) or restored (foreign inode) | The hold name is written on the receipt before `renameat2`. `RENAME_NOREPLACE` is required | A crash after the rename leaves the bytes under the journaled hold. The next reconciliation finishes unlink or restore. Missing source names are not success while a hold identity is outstanding | The cleanup worker is tracked. A timeout leaves the hold journal and the bytes in place | Foreign bytes are restored onto the original name only when that name is free. If it is occupied, the foreign bytes stay at the hold and `temp_hold` is kept. The new occupant is not overwritten |
| Final object | `{id}.tgbk` or `{id}.sha256` | rename of the publish inode, directory synced, checksum checked | The publish rename is durable at the `file` directory sync. Sync failure returns uncertain and does not release identities | If the final inode is the recorded temp inode (xattr) or clean inode (no xattr) and the checksum matches, the copy is success. Directory sync failure after that rename is a lost ack, not an abort | A late publisher still registered on the final name is not aborted | An inode that is not the recorded publish inode is not adopted |
| Durable receipt | one row per archive name and one per sidecar name | `pending`, `uncertain`, `rejected`, `observed`, `aborted`, or empty (confirmed) | `cleanup_verified` is written after the owned names are gone and the `delete` directory sync has observed them absent, and before `Get` of the final name. A failed `Get` does not clear that flag or the recorded identities | Restart reads the flag, retries `Get`, and only then records abort or success. Abort requires the flag, `Get` returning not found, and no busy publisher on the final, temp, or clean name | An active or abandoned-but-still-registered worker is never marked aborted. A transient `Get` leaves the verified flag for the next pass | `temp_conflict` plus `temp_hold` is kept when a foreign file could not be restored. That row is not removed and is not called complete cleanup. With no anchor and no hold, the row stays unresolved rather than discarding the proof |

Confirmed copies use an empty `state`. Pending, uncertain, rejected, observed, aborted, and any outstanding temp, clean, quarantine, hold, or conflict identity block local retention and destination removal.

## No-xattr publication

The source temp holds `TGVTMP1\n`, the 32 hex token, and a newline (41 bytes), then the payload. The clean file is a second inode. Its receipt identity is written while it is still empty. The payload is then copied from the source, starting after those 41 bytes, with the clean descriptor locked. Rename publishes that inode. The prefixed source and its anchor are removed only after the final directory sync, and their removal is itself directory-synced before the receipt drops those identities. A dispose or sync error stays on the receipt. It is returned as an uncertain publication error so the final object is not deleted as a rejected put.

## Disposition

`disposeRecorded` writes the quarantine name, renames the directory entry aside, and opens the hold.

- Owned inode: unlink the hold. The original name is already gone. A later sync has to observe every owned name absent before `cleanup_verified`.
- Foreign inode: `renameat2(RENAME_NOREPLACE)` back to the original name. `EEXIST` or a vanished source leaves the foreign bytes at the hold, sets `temp_hold` and `temp_conflict`, and does not unlink the anchor.
- The next full reconciliation, not a prefix sweep, is what finishes the anchor. It still does not overwrite a name that is now occupied, and it does not drop `temp_hold`.

A second pass is required when the first pass only records the conflict. The owned inode stays reachable through the anchor until that pass locks it and retires it.

## Abort and lost acknowledgement

Cleanup of the owned names is recorded before the final `Get`. If that `Get` times out, the row keeps `cleanup_verified` and the identities. The next reconciliation, including one after process restart, retries the read.

- Final name absent, cleanup verified, publisher not busy: `aborted`, then the rejected-absence path removes the row when nothing outstanding remains. Local delete, local retention, and destination removal can proceed.
- Final inode is the recorded publish inode: not an abort. A lost directory sync after rename stays a successful copy once the checksum matches. The sidecar is uploaded if it is still missing.
- Publisher busy, or the temp lock is busy: not an abort.

## Pre-journal residual

The receipt cannot name an inode that does not exist yet. Two gaps stay outside reconciliation. Neither is swept, because a sweep can delete a file this installation does not own. This is not one empty file.

1. Source temp, after `O_EXCL` create and before the receipt write. At most one inode and two directory entries (`.tmp` and, when `linkat` succeeded, `.own` to that same inode). Content is 0 bytes when the token is an xattr, or exactly 41 bytes of `TGVTMP1\n` + token + newline. No archive payload. The anchor adds a name, not a second copy of those bytes.
2. No-xattr clean file, after its create and before its own receipt write. The source temp is already journaled and may already hold the archive. The untracked extra is one directory entry of 0 bytes, with no token prefix and no payload.

`// ponytail:` on the source journal records that ceiling. Upgrade path: a reservation that returns an inode before a directory entry exists. Trigger: the platform can journal an inode that is not yet linked. `PONYTAIL-DEBT.md` records it as platform ceiling 14, and the ledger's debt count includes it.

## Syscall

`renameat2` is 316 on linux/amd64 and 276 on linux/arm64. The number is selected by the Go architecture file. An arm64 compile checks the arm64 constant. Execution of that syscall is claimed only where the arm64 test binary actually ran.
