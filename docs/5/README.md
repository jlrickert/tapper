# KEG specification (keg-spec)

High-level overview: [KEG](../5) (Knowledge Exchange Graph) is a minimal, storage-agnostic model for small, linkable knowledge units ("[nodes](../15)"). This page's single responsibility is to explain what KEG is and the goals it serves. Detailed operational, implementation, and policy guidance has been split into separate focused nodes (placeholders linked below) so each document can maintain a single responsibility and be easier to discover, test, and reuse.

## Summary

- [KEG](../5) models small content units ([nodes](../15)) that pair human-readable content ([README.md](../19)) with small machine-readable metadata ([meta.yaml](../18)).
- Nodes are identified by stable numeric ids so tooling can reference them reliably.
- Index artifacts ([dex/](../6)) provide deterministic, machine-friendly views ([nodes.tsv](../7), [tags](../17), [links](../14), [backlinks](../16), [changes](../8)).
- [KEG](../5) is storage-agnostic: filesystem-backed repos are the simplest deployment, but backends may implement an API that exposes equivalent semantics.

## Core concepts (brief)

- [Node](../15): the basic unit (id directory) containing [README.md](../19) and [meta.yaml](../18) plus optional attachments/images.
- Indices ([dex/](../6)): generated artifacts used for fast lookup and automation ([nodes.tsv](../7), [tags](../17), [links](../14), [backlinks](../16), [changes](../8)).
- [Keg file](../2): repository-level config (small YAML) describing repository aliases, includes for tools, and index metadata.
- Tags and canonical slug pages: use parenthetical slugs in titles (e.g., "[Keg tags (keg-tags)](../10)") for canonical tag docs that tooling can prefer.

---

Migration plan — focused documents (placeholders)
To keep this node focused, the detailed guidance that was previously in this document has been split into dedicated nodes. Each of the items below will be a focused document; for now they are linked to a placeholder node (../0). When those nodes are created, update the links to the new ids.

- [Storage layout & repository layout](../0) (migrate to a dedicated "Storage layout" node): ../0
- [CLI design & recommended commands](../0) (migrate to a dedicated "keg CLI" node): ../0
- [Index generation & dex semantics](../0) (migrate to "Index generation" node): ../0
- [Linking and keg:token resolution](../0) (migrate to "Link resolution" node): ../0
- [Node metadata (meta.yaml) authoritative rules](../0) (migrate to "Node meta" node): ../0
- [Implementation notes — API, types, backends, and language-specific guidance](../0) (migrate to "Implementation notes" node): ../0
- [Testing guidance and indexer tests (migrate to "Testing" node): ../0
- [Best practices, security, and privacy guidance](../0) (migrate to "Best practices & security" node): ../0
- [Integration notes for tools like Zeke and example workflows](../0) (migrate to "Integrations & examples" node): ../0

## Related canonical docs (existing)

- [Keg node (node layout & conventions)](../15)
- [Node meta (meta.yaml)](../18)
- [Keg tags (tag docs)](../10)
- [Tags index (dex/tags)](../17)
- [Links index (dex/links)](../14)
- [Backlinks index (dex/backlinks)](.16)
- [Nodes index (dex/nodes.tsv)](../7)
- [Keg index (indexer overview)](../6)
- [Zeke AI utility (integration examples)](../3)
- [Keg storage layout](../25)

If you depend on any of the migrated sections now, please open an issue describing which section you need created first and why. The placeholder at ../0 is temporary; when new focused nodes are published the links here should be updated to point at their real ids.
