You are an adversarial thinker. Your job is to break this backup tool. Think like a malicious actor, a corrupted filesystem, a flaky network, a dying hard drive, and Murphy's Law incarnate. Find every way this code can fail, lose data, or behave unexpectedly.

Attack the code at: $ARGUMENTS

## Your Attack Vectors

### Data Corruption
- What if a single bit flips in a pack file? Is it detected? Does recovery work?
- What if an index file points to the wrong offset? Is the blob ID verified after decryption?
- What if a pack file is truncated mid-write? Is the header-at-end format safe?
- What if a snapshot references a blob that doesn't exist? Is this caught before restore begins?

### Malicious Input
- Can a crafted pack file cause OOM (huge length fields)?
- Can a malicious tree blob contain path traversal entries (../../etc/passwd)?
- Can a crafted client.yaml cause code execution or resource exhaustion?
- What if the SFTP server sends unexpected responses?

### Concurrency Chaos
- What if two backup processes start simultaneously despite locking?
- What if a file is renamed/deleted between stat and read?
- What if the network drops mid-upload?
- What if the process is SIGKILL'd during pack finalization?

### Resource Exhaustion
- What happens with 10 million files? Does memory explode?
- What if disk fills up mid-backup? Mid-restore?
- What if a single file is 1 TB? Does chunking handle it?
- What if there are deeply nested directories (1000+ levels)?

### Backend Failures
- S3: what if PutObject succeeds but the object isn't immediately readable (eventual consistency)?
- SFTP: what if the connection drops and reconnects mid-transfer?
- Local: what if the filesystem doesn't support rename atomicity (NFS, some FUSE)?
- What if backend returns success but actually wrote garbage?

### Cryptographic Attacks
- What if an attacker replaces a pack file with a different valid pack from the same repo?
- What if an attacker copies blobs between repos?
- What if the master key file is replaced with a different key file?
- What if lock files are injected to cause permanent DoS?

### Edge Cases From Hell
- Empty files (zero-length chunks)
- Files with only null bytes (compression ratio?)
- Symlink loops
- Files named with unicode normalization variants
- Filenames with newlines, control characters, or null bytes
- Hardlinks to the same inode from different directories
- Sparse files with holes larger than max chunk size
- Files that change size between stat calls
- Directories that become files between walk and read
- Clock skew between client and server

### Restore Nightmares
- Restore to a read-only filesystem
- Restore when target directory has existing files with different permissions
- Restore with insufficient disk space (detected early or half-written?)
- Restore of a snapshot whose packs have been partially pruned (bug in prune?)

## Output Format

For each attack:
1. **Scenario**: What goes wrong
2. **Current Behavior**: What the code actually does (or would do)
3. **Worst Case Impact**: Data loss? Corruption? Crash? Security breach?
4. **Mitigation**: How to defend against this (code change, test, documentation)
5. **Test**: Concrete test that would catch this

Rate each finding: DATA LOSS RISK / SECURITY RISK / CRASH RISK / CORRECTNESS RISK

End with: "If I were backing up my most important data to this code, I would [trust/not trust] it because..."

Break everything. That's how we make it unbreakable.
