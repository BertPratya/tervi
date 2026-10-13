# Slice index

One row per slice. The format is described in [README.md](README.md).

| # | Slice | Status | Summary |
| --- | --- | --- | --- |
| 0001 | [Pairing](0001-pairing/spec.md) | done | Pair a worker computer with the server: the worker shows an approval link, the user approves in a browser and types the shown code into the terminal, and the worker saves its credential in the OS secret store. |
| 0002 | [Connection](0002-connection/spec.md) | in progress | A daemon on the paired computer runs in the background, keeps a connection to the server, and reconnects by itself; the server shows each machine Online or Offline; `tervi status`, `tervi connect`, and `tervi logs`. |
