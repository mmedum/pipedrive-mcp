<!-- Thanks for contributing. Fill in the sections that apply. -->

## Summary

<!-- One to three bullets on what changed and why. -->

## Linked issues

<!-- Closes #N, refs #M. -->

## Test plan

<!-- How you checked it. If a tool's behavior changed, paste a representative input and output, with nothing from a real Pipedrive account in it. -->

## Checklist

- [ ] `make check` passes locally.
- [ ] New behavior has tests.
- [ ] Nothing from a real Pipedrive account, and no token, in code, tests, fixtures, docs or logs.
- [ ] `CHANGELOG.md` has an entry under `[Unreleased]` for a user-visible change.
- [ ] A change to the tool surface is named in a `SCHEMA-CHANGE:` or `BREAKING CHANGE:` commit footer.
- [ ] Anything touching the write path or a response shape has had a `make integration-writes` run, and a change to text the model reads a `make evals` run.

<!--
Review findings: a fixed one is one line in the commit body. A declined
one goes here, with its reason, or becomes a present-tense comment at
the line it protects.
-->
