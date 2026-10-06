# 0001 Pairing — plan

Status: draft
Spec: [spec.md](spec.md)

## Flow

### Step 1 — The worker starts a pairing

| Question | Answer |
| --- | --- |
| Who → who | Worker → Server |
| Endpoint | `POST /api/v1/pairings` |
| Sends | `hostname` (≤ 64), `os_name` (≤ 64), `os_version` (≤ 32). Each one is optional. |
| Server does | Creates a pairing in the state *waiting for approval*, with an expiry 10 minutes later |
| If the request times out after it was sent | The worker does not retry. It says the pairing could not be started and the user can run the command again. A pairing the server did create expires by itself. |

**Values the worker cannot read** are left out. The terminal and the browser
show `unknown` in their place.

**Values longer than their limit** are truncated by the worker: a value that
fits is kept whole; a longer one keeps its first (limit − 1) characters and
ends with `…`, so the result is exactly at the limit. Limits count characters,
not bytes. Examples for a limit of 64: 64 characters stay unchanged; 65 become
63 characters + `…`.

**The server rejects** any value over its limit with "invalid input". The real
worker never sends one; the check protects against other callers.

## Technical decisions

- Resource-style paths (`POST /api/v1/pairings` creates a pairing) — the common
  REST convention; every endpoint in this plan follows the same style.
- The hostname is the name shown to the user — custom names come later.
- Hostname and OS name up to 64 characters, OS version up to 32 — 64 is the
  longest hostname Linux allows, and real OS names and versions are far shorter.
- No automatic retry after a start request times out — the server may already
  have created the pairing, and a retry could create a second one.

## Parked

To add later in this plan:

- Local check before step 1: already paired (R2).
- Local check before step 1: the OS secret store is usable (R3).
- What the server answers in step 1.
- R21: anyone with the approval link can approve.
- R11 vs R22: showing the pairing code again.

For a later slice (move to `slices/backlog.md` when this plan is done):

- Custom computer names chosen by the user.
