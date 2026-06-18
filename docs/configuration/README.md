# Configuration Overview

Tapper configuration answers one practical question: when a person or agent runs
`tap`, which shared memory should it use?

Tapper uses three configuration layers:

1. User config (`~/.config/tapper/config.yaml`)
2. Project config (`.tapper/config.yaml`)
3. Keg config (`<keg-root>/keg`)

User and project configs control target resolution. Keg config controls metadata
inside a specific keg.

## How Config Layers Work

- User config defines machine-wide hubs, credentials, and fallbacks.
- Project config applies in a repository and points that repo at the right
  organization or project keg.
- Keg config is content metadata inside a specific keg and is separate from
  user/project resolver settings.

The normal onboarding path is:

```bash
tap bootstrap --kind local --default-keg @local/personal
tap keg create @local/personal
tap use @local/personal --user
```

For a shared team setup, bootstrap a hosted or enterprise hub, authenticate, then
set the project default:

```bash
tap bootstrap --kind cloud
tap auth login
tap keg create @acme/engineering
tap use @acme/engineering
```

## Which File Should I Edit?

- Need machine defaults, hubs, and credentials: [User Config](user-config.md)
- Need repo-specific defaults for teammates: [Project Config](project-config.md)
- Need title/creator/links/indexes for a keg: [Keg Config](keg-config.md)

## Detailed Pages

- [User Config](user-config.md)
- [Project Config](project-config.md)
- [Keg Config](keg-config.md)
- [Resolution Order](resolution-order.md)
- [Configuration Examples](examples.md)
- [Flights](flights.md)
- [Troubleshooting](troubleshooting.md)
