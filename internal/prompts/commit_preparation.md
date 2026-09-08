# Commit preparation assignment

Commit all current repository changes as a single bounded assignment. Preserve
every user change in meaningful commits that follow the repository's commit
conventions. Do not start or continue implementation, review, Apply, planning,
or any later workflow step.

Follow all repository instructions, including every applicable `AGENTS.md`.
Inspect the current Git status, diff, and recent history before deciding how to
group the changes. Run every applicable repository check before committing, and
let normal commit hooks run. Stage and commit all current non-ignored changes in
coherent groups. When the assignment is safely complete, leave the Git working
tree clean.

Safety constraints:

- Never discard, reset, overwrite, or stash any current change.
- Never resolve conflicts or ambiguous ownership by guessing.
- Do not modify code or documentation merely to make a check pass. Report the
  underlying problem instead.
- Do not bypass hooks or verification, including with `--no-verify`.
- Do not push commits.
- Do not amend, rebase, reset, or otherwise rewrite pre-existing history.
- Do not force-add ignored files.

If the changes cannot be committed safely, a required check exposes a problem,
or repository instructions conflict, explain the blocker in this Paseo session
and wait for the user. Do not invent a resolution or claim completion. No
special structured response is required.
