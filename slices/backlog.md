# Backlog

Deferred work. When planning a new slice, the architect reads this list and
proposes items to include. The format is in [README.md](README.md#backlogmd).

| # | Item | Why later | Found in | Status |
| --- | --- | --- | --- | --- |
| B1 | Set `ReadHeaderTimeout` (and other timeouts) on the server's `http.Server` | Matters only once the server is reachable from other computers; needed before R21's limit is lifted | PR #7, review round 1 | open |
| B2 | Remove the duplicate `TestHealth` in `cmd/server/main_test.go`, which calls `server.New` with a nil logger; or make `server.New` default a nil logger | Harmless now; may panic once task 02 makes `pairing.Register` use the logger | PR #7, review round 1 | open |
| B3 | Add the error to the ping failure message in `TestDBTestIsolated` (`internal/db/dbtest/dbtest_test.go`) | Affects only one test's failure message | PR #7, review round 2 | open |
| B4 | Log the kind of each `500 internal_error` in `internal/pairing` (never the values), using `Deps.Logger` | No failure leaves a trace today; matters once someone runs the server for real | PR #8, review round 1 | open |
| B5 | Answer unknown paths and wrong methods with `{"error": "<code>"}` JSON instead of Go's plain-text `404`/`405` | Only API clients notice; no client relies on it yet | PR #8, review round 1 | open |
| B6 | `TestInvalidLink`: start a real pairing first, send accept and reject with wrong keys, and check the real row is unchanged | The current 0-rows check cannot fail | PR #8, review round 2 | open |
