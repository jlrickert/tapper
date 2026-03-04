# Query Expressions

Several `tap` commands accept a `--query` flag that filters nodes using boolean
expressions over tags and metadata attributes.

## Commands That Support `--query`

- `tap list --query EXPR`
- `tap tags --query EXPR`
- `tap cat --query EXPR`
- `tap rm --query EXPR`
- `tap import --query EXPR`

## Syntax

A query expression is built from **terms** combined with **operators**.

### Terms

A term is either a tag name or a key=value attribute predicate.

- **Tag**: a plain identifier that matches nodes carrying that tag.

  ```
  golang
  ```

- **Attribute predicate**: `key=value` matches nodes whose `meta.yaml` contains
  the given key with the given value.

  ```
  entity=plan
  ```

Tags are resolved from the dex index (fast). Attribute predicates scan each
node's `meta.yaml` (slower on large kegs).

### Operators

Operators combine terms into compound expressions. Precedence from highest to
lowest: `not`, `and`, `or`.

| Operator | Alternatives | Description |
|----------|-------------|-------------|
| `not`    | `!`         | Negation — matches nodes that do **not** satisfy the term |
| `and`    | `&&`        | Intersection — matches nodes satisfying **both** sides |
| `or`     | `\|\|`      | Union — matches nodes satisfying **either** side |

### Grouping

Use parentheses to override precedence:

```
(golang or rust) and entity=patch
```

### Quoting

Use single or double quotes around terms that contain special characters or
spaces:

```
"my-complex-tag"
'entity=some value'
```

Backslash escapes work inside quoted strings (`\"`, `\'`).

## Examples

List all nodes tagged `golang`:

```bash
tap list --query "golang"
```

List all plan entities:

```bash
tap list --query "entity=plan"
```

List plan entities that are also tagged `golang`:

```bash
tap list --query "entity=plan and golang"
```

List nodes that are either tricks or concepts:

```bash
tap list --query "entity=trick or entity=concept"
```

List nodes tagged `planned` but not tricks:

```bash
tap list --query "planned and not entity=trick"
```

List all nodes that are **not** concepts:

```bash
tap list --query "not entity=concept"
```

Group with parentheses:

```bash
tap list --query "(golang or rust) and entity=patch"
```

Remove nodes matching a query instead of listing node IDs:

```bash
tap rm --query "entity=draft and not shipped"
```

## Operator Precedence

Without parentheses, `not` binds tightest, then `and`, then `or`:

```
a or b and not c
```

is parsed as:

```
a or (b and (not c))
```

## Error Handling

Invalid expressions produce a parse error:

```bash
tap list --query "a and (b"
# Error: expected ')' before end of expression
```
