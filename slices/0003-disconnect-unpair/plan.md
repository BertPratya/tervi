# 0003 Disconnect and unpair — plan

Status: draft
Spec: [spec.md](spec.md)

The rest of this plan is written with the user after slice 0002 is built,
because it depends on 0002's code. Design decisions that do not depend on it
are recorded here as they are made.

## Technical decisions

- A computer's identity for recognizing it (R24) is a one-way hash of
  `/etc/machine-id` combined with a fixed tervi-specific value, so the same
  computer always gives the same identity, and the machine ID itself can't
  be worked back from it. The worker computes it each time it pairs and
  saves nothing; the server stores it on each machine — it survives a
  rename and the loss of tervi's own files, and systemd's documentation asks
  applications to use an application-specific value derived from the
  machine ID instead of the confidential ID itself.
  Not chosen: a random ID that tervi creates and keeps in a file — lost
  whenever tervi's files are, which is the case recognition is most needed
  for; the raw `/etc/machine-id` — systemd treats it as confidential, and
  sending it lets other software link the computer across services.
