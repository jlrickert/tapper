# Tapper flight switch

Switch this Claude connection's Tapper flight to `$ARGUMENTS`.

The plugin intercepts this user-only command before prompt expansion, asks for
human confirmation through the existing MCP connection, and prevents the
expanded command from reaching the model. It changes no Tapper configuration.

For a persistent project default, run `tap use --flight @namespace/+slug` or
the default-namespace shorthand `tap use +slug`, then open a new session.
