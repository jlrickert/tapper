# Project Config

Project configuration lives in `.tapper/config.yaml`. Tapper loads every such
file from the startup directory up to the filesystem root; deeper values
win over parent projects and the user baseline.

```yaml
hub: atlas
keg: "@foldwise/dev"
```

Use `tap use @foldwise/dev` to write the project's `keg`, or
`tap hub set-default atlas` to write its `hub`. `tap config edit` edits the
project file; `tap config --project` inspects the merged project layer.

Project `keg`, `hub`, and `flight` override the corresponding defaults from
the winning `kegMap` rule. Explicit flags and environment variables win over both.
See [Resolution Order](resolution-order.md).

Hub definitions and credentials belong in user configuration. A project may
select saved Hub names and set `kegMap`, qualified KEG references, Flight context,
and agent definitions. Project Hub definitions are stripped with a warning;
`--strict` treats that warning as an error.
