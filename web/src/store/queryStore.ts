import { create } from 'zustand';
import type { QueryResponse, PlanBundle, OptimizationStep, ErrorDetails } from '../types';

const HISTORY_KEY = 'qe_query_history';
const HISTORY_MAX = 20;

function loadHistory(): string[] {
  try {
    return JSON.parse(localStorage.getItem(HISTORY_KEY) ?? '[]');
  } catch {
    return [];
  }
}

function saveHistory(history: string[]) {
  localStorage.setItem(HISTORY_KEY, JSON.stringify(history));
}

interface QueryStore {
  sql: string;
  setSql: (sql: string) => void;

  result: QueryResponse | null;
  setResult: (r: QueryResponse | null) => void;

  isLoading: boolean;
  setLoading: (v: boolean) => void;

  error: string | null;
  setError: (e: string | null) => void;

  errorDetails: ErrorDetails | null;
  setErrorDetails: (d: ErrorDetails | null) => void;

  plans: PlanBundle | null;
  optimizationSteps: OptimizationStep[];

  /** AbortController for the in-flight query request. */
  abortController: AbortController | null;
  setAbortController: (c: AbortController | null) => void;
  cancelQuery: () => void;

  history: string[];
  addToHistory: (sql: string) => void;
  clearHistory: () => void;
}

export const useQueryStore = create<QueryStore>((set) => ({
  sql: 'SELECT status, COUNT(*) FROM orders GROUP BY status ORDER BY 2 DESC',
  setSql: (sql) => set({ sql }),

  result: null,
  setResult: (result) => set({ result, plans: result?.plan ?? null, optimizationSteps: result?.optimizationSteps ?? [] }),

  isLoading: false,
  setLoading: (isLoading) => set({ isLoading }),

  error: null,
  setError: (error) => set({ error }),

  errorDetails: null,
  setErrorDetails: (errorDetails) => set({ errorDetails }),

  plans: null,
  optimizationSteps: [],

  abortController: null,
  setAbortController: (abortController) => set({ abortController }),
  cancelQuery: () =>
    set((state) => {
      state.abortController?.abort();
      return { abortController: null, isLoading: false };
    }),

  history: loadHistory(),
  addToHistory: (sql) =>
    set((state) => {
      const trimmed = sql.trim();
      if (!trimmed) return state;
      // Deduplicate: remove previous occurrence of same query
      const next = [trimmed, ...state.history.filter((q) => q !== trimmed)].slice(0, HISTORY_MAX);
      saveHistory(next);
      return { history: next };
    }),
  clearHistory: () => {
    saveHistory([]);
    set({ history: [] });
  },
}));
