# Release Coordination

Coordinate a release across Accumulate Network repositories.

## Prerequisites

- Orchestrator CLI is built: `go build -o /tmp/orchestrator ./cmd/orchestrator/ > /tmp/orchestrator-build.log 2>&1`
- All repos are on their default branches and up to date
- You know which repo(s) are being released and the target version
- You have confirmed with the user that a release is intended

## Managed Repositories and Default Branches

| Repo | Default Branch | Language |
|------|---------------|----------|
| accumulate | develop | Go |
| staking | main | Go |
| devnet | main | Go |
| explorer | develop | JS |
| wallet | develop | JS |
| accman | main | Go |
| accnet | main | Go |
| liteclient | main | Go |
| kermit | main | JS |

## Steps

### 1. Verify all tests pass

Run tests across all repos that are part of the release:

```bash
/tmp/orchestrator test-all > /tmp/orchestrator-test-all.log 2>&1
tail -60 /tmp/orchestrator-test-all.log
```

Or test individual repos:

```bash
/tmp/orchestrator test <repo> > /tmp/orchestrator-test-<repo>.log 2>&1
tail -40 /tmp/orchestrator-test-<repo>.log
```

Do not proceed if any tests fail. Fix failures first (see `playbooks/bug-fix.md`).

### 2. Check for pending merge requests

Review open MRs that should be included in or excluded from the release:

```bash
# GitLab repos
cd /home/paul/go/src/gitlab.com/AccumulateNetwork/<repo> && glab mr list > /tmp/orchestrator-mrs-<repo>.log 2>&1
tail -20 /tmp/orchestrator-mrs-<repo>.log

# GitHub repos
cd /home/paul/go/src/github.com/PaulSnow/<repo> && gh pr list > /tmp/orchestrator-prs-<repo>.log 2>&1
tail -20 /tmp/orchestrator-prs-<repo>.log
```

Report any open MRs to the user and confirm whether they should be merged before release.

### 3. Verify clean working state

```bash
/tmp/orchestrator status > /tmp/orchestrator-status.log 2>&1
tail -40 /tmp/orchestrator-status.log
```

All repos being released must have a clean working tree (no uncommitted changes).

### 4. Review recent changes

Check what has changed since the last release:

```bash
# Find the last tag
git -C /home/paul/go/src/gitlab.com/AccumulateNetwork/<repo> tag --sort=-creatordate | head -5

# Log changes since last tag
git -C /home/paul/go/src/gitlab.com/AccumulateNetwork/<repo> log --oneline <last-tag>..HEAD > /tmp/orchestrator-changes-<repo>.log 2>&1
tail -40 /tmp/orchestrator-changes-<repo>.log
```

### 5. Version bump

Update version strings in the appropriate files. The location varies by repo:

- **Go repos:** Check for `version.go`, `main.go`, or `go.mod` version references.
- **JS repos:** Update `package.json` version field.

```bash
# Find version references
grep -r "version" /home/paul/go/src/gitlab.com/AccumulateNetwork/<repo>/cmd/ > /tmp/orchestrator-version-search.log 2>&1
```

### 6. Update changelog

If the repo has a CHANGELOG or CHANGES file, add an entry for the new version summarizing the changes from step 4.

### 7. Commit the version bump

```bash
git -C /home/paul/go/src/gitlab.com/AccumulateNetwork/<repo> add <version-files> <changelog>
git -C /home/paul/go/src/gitlab.com/AccumulateNetwork/<repo> commit -m "Release v<version>"
```

### 8. Tag the release (only when asked)

**Do not tag without the user's explicit approval.**

```bash
git -C /home/paul/go/src/gitlab.com/AccumulateNetwork/<repo> tag -a v<version> -m "Release v<version>"
```

### 9. Build verification

Build the release artifacts and verify they work:

```bash
# Go repos
go build -C /home/paul/go/src/gitlab.com/AccumulateNetwork/<repo> ./... > /tmp/orchestrator-release-build-<repo>.log 2>&1
tail -20 /tmp/orchestrator-release-build-<repo>.log

# JS repos
cd /home/paul/go/src/gitlab.com/AccumulateNetwork/<repo> && npm run build > /tmp/orchestrator-release-build-<repo>.log 2>&1
tail -20 /tmp/orchestrator-release-build-<repo>.log
```

Run the full test suite one more time:

```bash
/tmp/orchestrator test <repo> > /tmp/orchestrator-release-test-<repo>.log 2>&1
tail -40 /tmp/orchestrator-release-test-<repo>.log
```

### 10. Push (only when asked)

**Do not push unless the user explicitly asks.**

```bash
git -C /home/paul/go/src/gitlab.com/AccumulateNetwork/<repo> push > /tmp/orchestrator-push-<repo>.log 2>&1
git -C /home/paul/go/src/gitlab.com/AccumulateNetwork/<repo> push --tags > /tmp/orchestrator-push-tags-<repo>.log 2>&1
tail -10 /tmp/orchestrator-push-<repo>.log
tail -10 /tmp/orchestrator-push-tags-<repo>.log
```

## Safety reminders

- **Always redirect command output to log files** (`> /tmp/file.log 2>&1`).
- **Never reset blockchain data.** Do not run `devnet start`, `devnet reset`, or any `--reset` command.
- **Never push without being asked.** This includes tags.
- **Never force push to main/master/develop.**
- Double-check version numbers before tagging. Tags are hard to undo.

## Completion checklist

- [ ] All tests pass across affected repos
- [ ] Pending MRs reviewed and resolved
- [ ] Working tree is clean in all affected repos
- [ ] Recent changes reviewed and summarized
- [ ] Version bumped in appropriate files
- [ ] Changelog updated
- [ ] Version bump committed
- [ ] Release tag created (only if user approved)
- [ ] Build verification passed
- [ ] Final test run passed
- [ ] Pushed to remote (only if user asked)
