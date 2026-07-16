import ky from 'ky';
import type {
  QueryRequest,
  QueryResponse,
  ExplainResponse,
  SchemaResponse,
  StatsResponse,
} from '../types';

const base = ky.create({ prefix: '/api', timeout: 35_000 });

export const api = {
  query: (req: QueryRequest, signal?: AbortSignal): Promise<QueryResponse> =>
    base.post('query', { json: req, signal }).json(),

  explain: (sql: string, signal?: AbortSignal): Promise<ExplainResponse> =>
    base.post('explain', { json: { sql }, signal }).json(),

  schema: (): Promise<SchemaResponse> => base.get('schema').json(),

  stats: (): Promise<StatsResponse> => base.get('stats').json(),

  seed: (): Promise<{ status: string }> => base.post('schema/seed').json(),

  createTable: (sql: string): Promise<{ table: string }> =>
    base.post('schema/table', { json: { sql } }).json(),
};
