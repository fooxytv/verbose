# Finding something again

Searching a history that has got too big to scroll.


```console
$ verbose find -project powerbi "docker run"
4 hits for "docker run" in 2 of 8 sessions

2026-10-20 13:40  claude  powerbi-fabric-workspace-lifecycle  87709cfb-…
    step 605/1218  Ran NET=$(docker network ls --format '{{.Name}}' | head -1)
        command: docker run --rm --network "$NET" mcr.microsoft.com/azure-cli:latest \
```

The question usually arrives at a prompt rather than in an application, and
the answer is a session, a step number and the line it was on — so `R` and
`/` take you back to watching it. `-project` narrows, `-limit` bounds, `-json`
makes it machine-readable, and nothing matching exits 0 with a sentence saying
how much was searched.

Note the headline on that hit: `Ran NET=$(docker network ls …)`. The `docker
run` is on the **second line** of the command, so no amount of reading step
titles would have found it. One step in five on this machine runs a multi-line
command.

---

[← back to the README](../README.md)
