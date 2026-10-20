# Maintaining the Ichinose fork

This fork follows upstream stable releases while keeping project sponsorship and
donation surfaces disabled and installation/update URLs pointed at
`Ichinose520122/3x-ui`.

## Sponsor sources

`internal/web/service/panel/sponsor.go` and its upstream test file are retained
with the `upstream_sponsors` Go build constraint. Normal builds exclude both
files, so the sponsor fetcher is not compiled into released binaries. Keeping
the original source below that constraint allows upstream edits to merge without
repeated modify/delete conflicts.

Do not pass `-tags upstream_sponsors` or add that tag to `GOFLAGS`. The Linux and
Windows release builds reject inclusion of the sponsor backend.

Sponsor frontend components, navigation, HTTP routes, translations, generated API
entries, donation links and funding metadata remain removed. Retaining the
backend source does not restore those surfaces. MTProto users' own ad-tag setting
is protocol functionality and remains available.

## Updating

1. Fetch the upstream stable tag into a separate ref, such as
   `refs/tags/upstream-v3.10.2`, and merge that ref into this fork's `main`.
2. Preserve the build constraints above, removed sponsor/donation surfaces, and
   this fork's download URLs and stable release workflow when resolving conflicts.
   Check new upstream files and routes for new sponsorship integrations.
3. Run frontend type checking, lint, affected tests, and the production build.
   Regenerate schemas/OpenAPI if their sources changed.
4. Commit and push the merge. Create the fork's version tag on that merge commit,
   not on the upstream commit. Never move an already published release tag.
5. Run the release workflow for that tag if it did not trigger automatically.
   Publication waits for backend tests, all eight platform builds, and asset
   checksum verification.

Existing script installations can use this fork's `update.sh` in place after the
new stable release is published. Upstream changes are not synchronized
automatically.
