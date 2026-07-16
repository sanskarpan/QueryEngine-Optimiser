export interface QueryOptions {
  explain?: boolean;
  includeStats?: boolean;
}

export interface QueryRequest {
  sql: string;
  options?: QueryOptions;
}

export interface OptimizationStep {
  rule: string;
  applied: boolean;
  description: string;
}

export interface ExecStats {
  rowsScanned: number;
  hashJoins: number;
  sortOperations: number;
  rowsProduced: number;
}

export interface PlanBundle {
  logical: PlanNode;
  optimized: PlanNode;
  physical: PlanNode;
}

export interface PlanNode {
  id: string;
  type: string;
  estimatedRows?: number;
  estimatedCost?: number;
  attributes?: Record<string, unknown>;
  children?: PlanNode[];
}

export interface QueryResponse {
  columns: string[];
  rows: (string | number | boolean | null)[][];
  rowCount: number;
  executionTimeMs: number;
  plan?: PlanBundle;
  optimizationSteps?: OptimizationStep[];
  stats?: ExecStats;
  error?: string;
}

export interface ExplainResponse {
  plan: PlanBundle;
  error?: string;
}

export interface ColumnInfo {
  name: string;
  type: string;
  nullable: boolean;
  primaryKey: boolean;
}

export interface TableInfo {
  name: string;
  columns: ColumnInfo[];
  rowCount: number;
}

export interface SchemaResponse {
  tables: TableInfo[];
}

export interface BucketDTO {
  low: string;
  high: string;
  frequency: number;
}

export interface ColumnStatsDTO {
  distinctCount: number;
  nullCount: number;
  minValue: string | null;
  maxValue: string | null;
  histogram: BucketDTO[];
}

export interface TableStatsDTO {
  rowCount: number;
  pageCount: number;
  columns: Record<string, ColumnStatsDTO>;
}

export interface StatsResponse {
  tables: Record<string, TableStatsDTO>;
}

export interface ErrorDetails {
  message: string;
  line: number;
  col: number;
  stage: string;
}
