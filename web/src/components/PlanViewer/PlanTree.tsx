import { useEffect, useCallback, useState, useRef } from 'react';
import {
  ReactFlow,
  Background,
  Controls,
  useNodesState,
  useEdgesState,
  type Node,
  type Edge,
  type NodeMouseHandler,
} from '@xyflow/react';
import '@xyflow/react/dist/style.css';
import dagre from '@dagrejs/dagre';
import { toPng } from 'html-to-image';
import type { PlanNode } from '../../types';
import { useQueryStore } from '../../store/queryStore';

const NODE_W = 200;
const NODE_H = 80;

const NODE_COLORS: Record<string, string> = {
  Scan: '#1d4ed8',
  Filter: '#374151',
  Project: '#374151',
  Join: '#92400e',
  Aggregate: '#5b21b6',
  Sort: '#0f766e',
  Limit: '#0f766e',
  EmptyRelation: '#374151',
  HashJoin: '#92400e',
  NestedLoopJoin: '#92400e',
  HashAggregate: '#5b21b6',
  Projection: '#374151',
  SeqScan: '#1d4ed8',
};

function getColor(type: string): string {
  for (const [key, color] of Object.entries(NODE_COLORS)) {
    if (type.toLowerCase().includes(key.toLowerCase())) return color;
  }
  return '#374151';
}

function collectMaxCost(node: PlanNode): number {
  let max = node.estimatedCost ?? 0;
  for (const child of node.children ?? []) {
    max = Math.max(max, collectMaxCost(child));
  }
  return max;
}

function flattenPlan(
  node: PlanNode,
  nodes: Node[],
  edges: Edge[],
  maxCost: number,
  parentId?: string,
) {
  const id = node.id ?? `node-${nodes.length}`;
  const color = getColor(node.type ?? '');
  const costPct = maxCost > 0 && node.estimatedCost != null
    ? Math.max(4, Math.round((node.estimatedCost / maxCost) * 100))
    : 0;

  nodes.push({
    id,
    type: 'default',
    position: { x: 0, y: 0 },
    data: {
      planNode: node,
      label: (
        <div className="text-left px-2 py-1 w-full">
          <div className="font-semibold text-xs text-white truncate">{node.type}</div>
          {node.attributes && (
            <div className="text-[10px] text-gray-300 mt-0.5 truncate">
              {Object.entries(node.attributes)
                .filter(([, v]) => v != null && v !== '')
                .map(([k, v]) => `${k}: ${v}`)
                .join(' · ')}
            </div>
          )}
          {costPct > 0 && (
            <div className="mt-1.5">
              <div className="w-full bg-black/30 rounded-full h-1">
                <div
                  className="bg-indigo-300/80 h-1 rounded-full transition-all"
                  style={{ width: `${costPct}%` }}
                />
              </div>
              <div className="flex gap-2 text-[9px] text-gray-400 mt-0.5">
                <span>cost {node.estimatedCost?.toFixed(1)}</span>
                {node.estimatedRows != null && <span>· {node.estimatedRows} rows</span>}
              </div>
            </div>
          )}
        </div>
      ),
    },
    style: {
      background: color,
      border: '1px solid rgba(255,255,255,0.15)',
      borderRadius: '8px',
      width: NODE_W,
      minHeight: NODE_H,
      color: '#fff',
      fontSize: '12px',
      padding: 0,
      cursor: 'pointer',
    },
  });

  if (parentId) {
    edges.push({
      id: `${parentId}-${id}`,
      source: parentId,
      target: id,
      style: { stroke: '#4b5563', strokeWidth: 1.5 },
    });
  }

  for (const child of node.children ?? []) {
    flattenPlan(child, nodes, edges, maxCost, id);
  }
}

function applyDagreLayout(nodes: Node[], edges: Edge[]) {
  const g = new dagre.graphlib.Graph();
  g.setGraph({ rankdir: 'TB', nodesep: 24, ranksep: 48 });
  g.setDefaultEdgeLabel(() => ({}));

  for (const n of nodes) {
    g.setNode(n.id, { width: NODE_W, height: NODE_H });
  }
  for (const e of edges) {
    g.setEdge(e.source, e.target);
  }
  dagre.layout(g);

  return nodes.map((n) => {
    const pos = g.node(n.id);
    return { ...n, position: { x: pos.x - NODE_W / 2, y: pos.y - NODE_H / 2 } };
  });
}

interface Props {
  plan: PlanNode | null;
  showOptHints?: boolean;
}

export function PlanTree({ plan, showOptHints }: Props) {
  const { isLoading, optimizationSteps } = useQueryStore();
  const appliedRules = showOptHints ? (optimizationSteps ?? []).filter((s) => s.applied) : [];
  const [nodes, setNodes, onNodesChange] = useNodesState<Node>([]);
  const [edges, setEdges, onEdgesChange] = useEdgesState<Edge>([]);
  const [selectedPlanNode, setSelectedPlanNode] = useState<PlanNode | null>(null);
  const containerRef = useRef<HTMLDivElement>(null);

  const exportPng = useCallback(() => {
    const el = containerRef.current;
    if (!el) return;
    toPng(el, { backgroundColor: '#1a1d27', pixelRatio: 2 })
      .then((dataUrl) => {
        const a = document.createElement('a');
        a.href = dataUrl;
        a.download = 'query-plan.png';
        a.click();
      })
      .catch(console.error);
  }, []);

  const buildGraph = useCallback((p: PlanNode) => {
    const ns: Node[] = [];
    const es: Edge[] = [];
    const maxCost = collectMaxCost(p);
    flattenPlan(p, ns, es, maxCost);
    const laid = applyDagreLayout(ns, es);
    setNodes(laid);
    setEdges(es);
    setSelectedPlanNode(null);
  }, [setNodes, setEdges]);

  useEffect(() => {
    if (plan) buildGraph(plan);
    else { setNodes([]); setEdges([]); setSelectedPlanNode(null); }
  }, [plan, buildGraph, setNodes, setEdges]);

  const onNodeClick: NodeMouseHandler = useCallback((_evt, node) => {
    const pn = node.data?.planNode as PlanNode | undefined;
    setSelectedPlanNode(pn ?? null);
  }, []);

  if (isLoading) {
    return (
      <div className="flex items-center justify-center h-full text-[#8892a4] text-sm">
        <div className="flex flex-col items-center gap-2">
          <div className="w-6 h-6 border-2 border-indigo-500 border-t-transparent rounded-full animate-spin" />
          <span>Building plan...</span>
        </div>
      </div>
    );
  }

  if (!plan) {
    return (
      <div className="flex items-center justify-center h-full text-[#8892a4] text-sm">
        No plan available
      </div>
    );
  }

  return (
    <div ref={containerRef} className="h-full w-full relative">
      <button
        onClick={exportPng}
        title="Export plan as PNG"
        className="absolute top-2 left-2 z-10 text-xs text-[#8892a4] hover:text-white bg-[#1a1d27]/80 border border-[#2e3347] hover:border-indigo-500 rounded px-2 py-1 transition-colors"
      >
        Export PNG
      </button>

      {appliedRules.length > 0 && (
        <div className="absolute top-2 left-1/2 -translate-x-1/2 z-10 flex items-center gap-1.5 bg-[#1a1d27]/90 border border-green-800/50 rounded-full px-3 py-1 backdrop-blur-sm max-w-[70%] overflow-x-auto">
          <span className="text-green-400 text-[10px] font-semibold uppercase tracking-wide shrink-0">
            {appliedRules.length} opt{appliedRules.length !== 1 ? 's' : ''}:
          </span>
          {appliedRules.map((s, i) => (
            <span
              key={i}
              title={s.description}
              className="text-[10px] text-green-300 bg-green-900/30 border border-green-800/40 rounded px-1.5 py-0.5 whitespace-nowrap cursor-default shrink-0"
            >
              {s.rule}
            </span>
          ))}
        </div>
      )}
      <ReactFlow
        nodes={nodes}
        edges={edges}
        onNodesChange={onNodesChange}
        onEdgesChange={onEdgesChange}
        onNodeClick={onNodeClick}
        fitView
        fitViewOptions={{ padding: 0.2 }}
        nodesDraggable={false}
        nodesConnectable={false}
        elementsSelectable={true}
      >
        <Background color="#2e3347" gap={16} />
        <Controls />
      </ReactFlow>

      {/* Node detail sidebar */}
      {selectedPlanNode && (
        <div className="absolute right-0 top-0 h-full w-60 bg-[#1a1d27]/95 backdrop-blur-sm border-l border-[#2e3347] flex flex-col z-20 shadow-xl">
          <div className="flex items-center justify-between px-3 py-2 border-b border-[#2e3347] shrink-0">
            <span className="font-semibold text-sm truncate">{selectedPlanNode.type}</span>
            <button
              onClick={() => setSelectedPlanNode(null)}
              className="text-[#8892a4] hover:text-white text-lg leading-none ml-2"
            >
              ×
            </button>
          </div>
          <div className="flex-1 overflow-auto p-3 space-y-2 text-xs">
            {selectedPlanNode.estimatedRows != null && (
              <Row label="Est. rows" value={String(selectedPlanNode.estimatedRows)} />
            )}
            {selectedPlanNode.estimatedCost != null && (
              <Row label="Est. cost" value={selectedPlanNode.estimatedCost.toFixed(3)} />
            )}
            {selectedPlanNode.attributes &&
              Object.entries(selectedPlanNode.attributes)
                .filter(([, v]) => v != null && v !== '')
                .map(([k, v]) => (
                  <Row key={k} label={k} value={String(v)} />
                ))}
          </div>
        </div>
      )}
    </div>
  );
}

function Row({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <div className="text-[#8892a4] uppercase tracking-wide text-[10px] mb-0.5">{label}</div>
      <div className="font-mono text-xs text-[#e2e8f0] break-all">{value}</div>
    </div>
  );
}
