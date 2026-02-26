# Configuration Overview

tapper uses three configuration layers:

1. User config (`~/.config/tapper/config.yaml`)
2. Project config (`.tapper/config.yaml`)
3. Keg config (`<keg-root>/keg`)

User and project configs control target resolution and aliases. Keg config controls metadata
inside a specific keg.

## How Config Layers Work

- User config defines machine-wide defaults.
- Project config applies in a repository and overrides user config values where applicable.
- Keg config is per-keg content and is separate from user/project resolver settings.

## Which File Should I Edit?

- Need machine defaults and discovery paths: [User Config](user-config.md)
- Need repo-specific defaults for teammates: [Project Config](project-config.md)
- Need title/creator/links/indexes for a keg: [Keg Config](keg-config.md)

## Detailed Pages

- [User Config](user-config.md)
- [Project Config](project-config.md)
- [Keg Config](keg-config.md)
- [Resolution Order](resolution-order.md)
- [Configuration Examples](examples.md)
- [Troubleshooting](troubleshooting.md)
