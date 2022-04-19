# ghal

TODO: this readme.

Needs:

* GITHUB_TOKEN environment variable (personal access token)
* GITHUB_USER_SESSION env var (from `user_session` browser cookie)

Installation: `brew install aidansteele/taps/ghal`

Usage: 

* `cd` to a repo
* `ghal e2e.yml deploy` to tail the `deploy` job from the `.github/workflows/e2e.yml` workflow.
* `ctrl+c` or `q` to quit

New builds will automatically start streaming and replace inflight builds.
