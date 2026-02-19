---
name: release
description: Create a bamgate release — update STATUS.md, commit, tag, push, and create a GitHub release with correct flags
---

## When to use

Use this skill whenever the user asks to release, ship, tag, or publish a new version of bamgate.

## Release procedure

Follow these steps exactly, in order:

### 1. Determine the version number

- Look at the latest git tag: `git tag --sort=-creatordate | head -1`
- Bump the patch version (e.g., v1.15.6 -> v1.15.7) unless the user specifies otherwise
- Ask the user to confirm the version if there is any ambiguity

### 2. Update STATUS.md

Before committing, update `STATUS.md`:

- Update the `Last updated:` line at the top with today's date and session number
- Add a row to the **Releases** table (near line 122+) with the new version, today's date, and a short highlight summary
- Add a row to the **Changelog** table (near line 168+) with the session number, today's date, and a detailed summary of what changed

### 3. Commit

- Stage all changes: `git add -A`
- Write a clear commit message summarizing the changes (not just "release vX.Y.Z" — describe what changed)
- Commit: `git commit -m "..."`

### 4. Tag

- Create an annotated-style lightweight tag: `git tag vX.Y.Z`

### 5. Push

- Push commit and tag together: `git push && git push --tags`

### 6. Create GitHub release

Use `gh release create` with these **exact flags**:

```bash
# Write release notes to a temp file first
cat <<'EOF' > /tmp/release-notes.md
## Summary headline

Description of changes...
EOF

# Create the release using --notes-file (NOT --body, which does not exist)
gh release create vX.Y.Z --title "vX.Y.Z" --notes-file /tmp/release-notes.md
```

**IMPORTANT flag notes:**
- Use `--notes-file` (`-F`) to pass release notes from a file
- Or use `--notes` (`-n`) for inline short notes
- There is NO `--body` flag — using it will fail
- The release notes should include: a summary headline, what changed, and why

### 7. Report the release URL

After `gh release create` succeeds, it prints the release URL. Show this to the user.

## What happens after

GitHub Actions will automatically:
- Build binaries via GoReleaser (linux/darwin, amd64/arm64)
- Build the Android AAR + debug APK
- Attach all artifacts to the release

The version string is injected at build time via `-ldflags "-X main.version={{.Version}}"`.
