import Graph from "graphology";
import forceAtlas2 from "graphology-layout-forceatlas2";
import Sigma from "sigma";

type GraphNode = {
  id: string;
  label: string;
  summary: string;
  tags: string[];
  url: string;
};

type GraphEdge = {
  source: string;
  target: string;
  type: "link" | "backlink" | string;
};

type GraphPayload = {
  nodes: GraphNode[];
  edges: GraphEdge[];
};

declare global {
  interface Window {
    __KEG__?: GraphPayload;
  }
}

const EMPTY_PAYLOAD: GraphPayload = { nodes: [], edges: [] };

function safePayload(): GraphPayload {
  const raw = window.__KEG__;
  if (!raw || !Array.isArray(raw.nodes) || !Array.isArray(raw.edges)) {
    return EMPTY_PAYLOAD;
  }
  return raw;
}

function ensurePanel(): HTMLElement {
  const existing = document.getElementById("panel");
  if (existing) return existing;

  const panel = document.createElement("aside");
  panel.id = "panel";
  panel.className = "hidden";
  document.body.appendChild(panel);
  return panel;
}

function renderEmpty(container: HTMLElement): void {
  container.innerHTML = `
    <div style="padding:24px;font-family:monospace">
      <h2 style="margin:0 0 8px 0">KEG Graph</h2>
      <p style="margin:0;color:#4b5563">No nodes found in dex indexes.</p>
    </div>
  `;
}

function toNodeMap(payload: GraphPayload): Map<string, GraphNode> {
  const out = new Map<string, GraphNode>();
  for (const node of payload.nodes) {
    if (!node || typeof node.id !== "string" || node.id.trim() === "") continue;
    out.set(node.id, {
      id: node.id,
      label: node.label || node.id,
      summary: node.summary || "",
      tags: Array.isArray(node.tags) ? node.tags : [],
      url: node.url || "",
    });
  }
  return out;
}

function edgeColor(edgeType: string): string {
  if (edgeType === "backlink") return "rgba(71, 85, 105, 0.42)";
  return "rgba(30, 64, 175, 0.62)";
}

function edgeSigmaType(edgeType: string): "arrow" | "line" {
  if (edgeType === "backlink") return "line";
  return "arrow";
}

function init(): void {
  const container = document.getElementById("app");
  if (!container) return;

  const payload = safePayload();
  if (payload.nodes.length === 0) {
    renderEmpty(container);
    return;
  }

  const nodeMap = toNodeMap(payload);
  const graph = new Graph({ multi: true, type: "directed" });
  const degree = new Map<string, number>();

  const nodes = Array.from(nodeMap.values());
  const total = nodes.length;
  const radius = Math.max(20, Math.sqrt(total) * 12);

  nodes.forEach((node, index) => {
    const angle = (index / Math.max(total, 1)) * Math.PI * 2;
    const ring = 1 + Math.floor(index / 180);
    const x = Math.cos(angle) * radius * ring * 0.25;
    const y = Math.sin(angle) * radius * ring * 0.25;

    degree.set(node.id, 0);
    graph.addNode(node.id, {
      x,
      y,
      label: node.label || node.id,
      size: 4,
      color: "#1f5aa6",
      data: node,
    });
  });

  let edgeCount = 0;
  payload.edges.forEach((edge, i) => {
    if (!edge || !edge.source || !edge.target) return;

    if (!graph.hasNode(edge.source)) {
      graph.addNode(edge.source, {
        x: 0,
        y: 0,
        label: edge.source,
        size: 3,
        color: "#64748b",
        data: { id: edge.source, label: edge.source, summary: "", tags: [], url: "" },
      });
      degree.set(edge.source, degree.get(edge.source) ?? 0);
    }
    if (!graph.hasNode(edge.target)) {
      graph.addNode(edge.target, {
        x: 0,
        y: 0,
        label: edge.target,
        size: 3,
        color: "#64748b",
        data: { id: edge.target, label: edge.target, summary: "", tags: [], url: "" },
      });
      degree.set(edge.target, degree.get(edge.target) ?? 0);
    }

    const key = `${edge.source}->${edge.target}:${edge.type}:${i}`;
    graph.addEdgeWithKey(key, edge.source, edge.target, {
      color: edgeColor(edge.type),
      size: edge.type === "backlink" ? 0.55 : 0.95,
      type: edgeSigmaType(edge.type),
      data: edge,
    });

    degree.set(edge.source, (degree.get(edge.source) ?? 0) + 1);
    degree.set(edge.target, (degree.get(edge.target) ?? 0) + 1);
    edgeCount++;
  });

  graph.forEachNode((nodeId) => {
    const d = degree.get(nodeId) ?? 0;
    graph.setNodeAttribute(nodeId, "size", 2.4 + Math.min(10, Math.sqrt(d + 1)));
  });

  if (graph.order > 1 && graph.order <= 2600 && edgeCount > 0) {
    forceAtlas2.assign(graph, {
      iterations: 80,
      settings: forceAtlas2.inferSettings(graph),
    });
  }

  const panel = ensurePanel();

  const renderer = new Sigma(graph, container, {
    renderLabels: true,
    labelRenderedSizeThreshold: 9,
    defaultEdgeType: "arrow",
    defaultNodeColor: "#1f5aa6",
    defaultEdgeColor: "rgba(30, 64, 175, 0.62)",
    defaultDrawEdgeLabels: false,
    enableEdgeEvents: false,
  });

  function hidePanel() {
    panel.classList.add("hidden");
    panel.innerHTML = "";
  }

  function showPanel(nodeId: string) {
    const attrs = graph.getNodeAttributes(nodeId) as {
      data?: GraphNode;
      label?: string;
    };
    const nodeData = attrs.data ?? {
      id: nodeId,
      label: attrs.label || nodeId,
      summary: "",
      tags: [],
      url: "",
    };

    const outDegree = graph.outDegree(nodeId);
    const inDegree = graph.inDegree(nodeId);
    const tags = Array.isArray(nodeData.tags) ? nodeData.tags : [];
    const safeTags = tags.length > 0 ? tags.join(", ") : "none";
    const safeSummary = nodeData.summary?.trim() || "No summary available.";

    const linkBlock =
      nodeData.url && nodeData.url.trim() !== ""
        ? `<p class="meta"><a href="${nodeData.url}" target="_blank" rel="noopener noreferrer">Open node</a></p>`
        : "";

    panel.innerHTML = `
      <h2>${nodeData.label || nodeData.id}</h2>
      <p>${safeSummary}</p>
      <p class="meta"><strong>ID:</strong> <code>${nodeData.id}</code></p>
      <p class="meta"><strong>Tags:</strong> ${safeTags}</p>
      <p class="meta"><strong>Outgoing:</strong> ${outDegree} &nbsp;&nbsp; <strong>Incoming:</strong> ${inDegree}</p>
      ${linkBlock}
    `;
    panel.classList.remove("hidden");
  }

  renderer.on("clickNode", ({ node }) => {
    showPanel(node);
  });

  renderer.on("clickStage", () => {
    hidePanel();
  });
}

init();

