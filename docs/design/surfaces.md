## Running a pack unattended

A pack run by a scheduler is the CLI surface with no one at the terminal: stdin is closed or the
null device. Any tool call that no `-permission-rules` entry decides falls through to the
terminal prompt, which then denies at once. The step fails rather than hangs
(`TestAnUnattendedAskDeniesInsteadOfHanging`).

So a scheduled pack must be invoked with `-permission-rules` covering every tool call its agent
may make. The last rule should be an explicit catch-all, allow or deny, so nothing reaches the
prompt:

```
atlas -pack packs/<pack>.yaml -permission-rules 'notes.write:edit:allow,*:*:deny'
```

A second scheduled run while the first is still going is refused at startup, naming the first
run's pid (`.atlas.lock`). A scheduler should treat that as "already running", not as a failure
to retry in a loop.
