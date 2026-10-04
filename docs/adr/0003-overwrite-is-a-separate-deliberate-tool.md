# Overwriting a file is a separate, deliberate tool

There is no upsert `write`. `create` fails if the file exists and `force_write` fails if it doesn't, so an agent can't clobber a file by accident: it has to notice the file is already there and choose to replace it wholesale. The friction is meant to push agents toward surgical tools (`edit`, `upsert_section`, `append_to_section`). It's unverified whether it actually changes agent behavior. Revisit with evidence rather than merging the tools back for tidiness.
