/* Gortex — seed data
 * Mirrors the design's data.jsx fixture so pages render with realistic
 * shapes when the live API hasn't filled them in yet. Replace gradually
 * with real /v1/* responses (api.ts wires those).
 */

export type Repo = {
  id: string
  owner: string
  lang: string
  nodes: number
  edges: number
  funcs: number
  types: number
  methods: number
  interfaces: number
  vars: number
  files: number
  color: string
}

export const REPOS: Repo[] = [
  { id: 'core-api',     owner: 'acme', lang: 'go',   nodes: 784,   edges: 6354,  funcs: 223,  types: 212, methods: 193,  interfaces: 70, vars: 219,  files: 62,  color: 'oklch(0.82 0.15 155)' },
  { id: 'tuck_app',     owner: 'acme', lang: 'dart', nodes: 10905, edges: 54108, funcs: 4003, types: 733, methods: 2852, interfaces: 83, vars: 3143, files: 812, color: 'oklch(0.80 0.13 230)' },
  { id: 'worker',       owner: 'acme', lang: 'go',   nodes: 745,   edges: 6060,  funcs: 277,  types: 81,  methods: 14,   interfaces: 37, vars: 335,  files: 58,  color: 'oklch(0.78 0.14 300)' },
  { id: 'web',          owner: 'acme', lang: 'ts',   nodes: 765,   edges: 0,     funcs: 201,  types: 126, methods: 8,    interfaces: 1,  vars: 287,  files: 74,  color: 'oklch(0.80 0.17 10)' },
  { id: 'infra',        owner: 'acme', lang: 'hcl',  nodes: 315,   edges: 557,   funcs: 47,   types: 81,  methods: 1,    interfaces: 1,  vars: 117,  files: 40,  color: 'oklch(0.82 0.14 45)' },
  { id: 'extension',    owner: 'acme', lang: 'ts',   nodes: 396,   edges: 0,     funcs: 83,   types: 12,  methods: 0,    interfaces: 1,  vars: 83,   files: 38,  color: 'oklch(0.82 0.15 80)' },
  { id: 'email-worker', owner: 'acme', lang: 'go',   nodes: 98,    edges: 79,    funcs: 11,   types: 8,   methods: 4,    interfaces: 1,  vars: 60,   files: 14,  color: 'oklch(0.72 0.02 252)' },
  { id: 'pkg',          owner: 'acme', lang: 'go',   nodes: 77,    edges: 210,   funcs: 8,    types: 3,   methods: 4,    interfaces: 1,  vars: 0,    files: 10,  color: 'oklch(0.72 0.02 252)' },
]

export type Language = { name: string; bytes: number; color: string }
export const LANGUAGES: Language[] = [
  { name: 'go',         bytes: 5682, color: 'oklch(0.72 0.12 215)' },
  { name: 'dart',       bytes: 1019, color: 'oklch(0.72 0.12 240)' },
  { name: 'typescript', bytes: 576,  color: 'oklch(0.72 0.15 255)' },
  { name: 'objc',       bytes: 1041, color: 'oklch(0.80 0.13 230)' },
  { name: 'swift',      bytes: 1036, color: 'oklch(0.78 0.17 30)' },
  { name: 'markdown',   bytes: 752,  color: 'oklch(0.70 0.01 252)' },
  { name: 'json',       bytes: 383,  color: 'oklch(0.78 0.14 80)' },
  { name: 'html',       bytes: 383,  color: 'oklch(0.72 0.16 15)' },
  { name: 'yaml',       bytes: 124,  color: 'oklch(0.78 0.14 300)' },
  { name: 'c',          bytes: 85,   color: 'oklch(0.68 0.11 260)' },
  { name: 'css',        bytes: 68,   color: 'oklch(0.72 0.17 310)' },
  { name: 'other',      bytes: 540,  color: 'oklch(0.55 0.02 252)' },
]

export type NodeKind = { name: string; count: number; color: string; cssVar: string }
export const NODE_KINDS: NodeKind[] = [
  { name: 'function',  count: 48110, color: 'var(--k-function)',  cssVar: '--k-function' },
  { name: 'method',    count: 32180, color: 'var(--k-method)',    cssVar: '--k-method' },
  { name: 'type',      count: 8420,  color: 'var(--k-type)',      cssVar: '--k-type' },
  { name: 'interface', count: 412,   color: 'var(--k-interface)', cssVar: '--k-interface' },
  { name: 'variable',  count: 18290, color: 'var(--k-variable)',  cssVar: '--k-variable' },
  { name: 'file',      count: 1108,  color: 'var(--k-file)',      cssVar: '--k-file' },
  { name: 'import',    count: 5420,  color: 'var(--k-import)',    cssVar: '--k-import' },
  { name: 'contract',  count: 188,   color: 'var(--k-contract)',  cssVar: '--k-contract' },
]

export const STATS = {
  status: 'ok',
  indexed: true,
  version: 'v0.19.2',
  uptimeMinutes: 33,
  uptimeText: '2003s total',
  totalNodes: 13963,
  totalEdges: 72418,
  avgEdgesPerNode: 5.2,
  reposIndexed: 8,
  communities: 384,
  processes: 127,
  contracts: 188,
  caveats: 42,
  deadCode: 217,
  cycles: 6,
}

export type Symbol = {
  id: string
  kind: 'function' | 'method' | 'type' | 'interface' | 'variable'
  name: string
  repo: string
  file: string
  sig: string
  callers: number
  callees: number
  community: string
  caveats: string[]
}
export const SYMBOLS: Symbol[] = [
  { id: 'core-api:internal/http/handler.RegisterRoutes',  kind: 'function', name: 'RegisterRoutes',     repo: 'core-api', file: 'internal/http/handler.go:24',   sig: 'func RegisterRoutes(r *chi.Mux, deps *App) error', callers: 3, callees: 42, community: 'http',   caveats: ['hot', 'boundary'] },
  { id: 'core-api:internal/ingest.EmailIngestHandler',    kind: 'method',   name: 'EmailIngestHandler', repo: 'core-api', file: 'internal/ingest/email.go:112',  sig: 'func (s *Service) EmailIngestHandler(ctx context.Context, m *InboundEmail) error', callers: 2, callees: 18, community: 'ingest', caveats: ['hot'] },
  { id: 'core-api:internal/store.PostgresTuckStore',      kind: 'type',     name: 'PostgresTuckStore',  repo: 'core-api', file: 'internal/store/postgres.go:18', sig: 'type PostgresTuckStore struct { pool *pgxpool.Pool; log *slog.Logger }', callers: 0, callees: 0, community: 'store', caveats: [] },
  { id: 'core-api:internal/link.ExtractLinks',            kind: 'function', name: 'ExtractLinks',       repo: 'core-api', file: 'internal/link/extractor.go:42', sig: 'func ExtractLinks(ctx context.Context, body []byte) ([]Link, error)', callers: 5, callees: 9, community: 'ingest', caveats: ['deprecated'] },
  { id: 'tuck_app:lib/features/tucks/tuck_repository.dart#TuckRepository', kind: 'type', name: 'TuckRepository', repo: 'tuck_app', file: 'lib/features/tucks/tuck_repository.dart:12', sig: 'class TuckRepository extends BaseRepository<Tuck>', callers: 0, callees: 0, community: 'tucks', caveats: ['unowned'] },
  { id: 'worker:internal/queue.Processor',                kind: 'type',     name: 'Processor',          repo: 'worker',   file: 'internal/queue/processor.go:8',  sig: 'type Processor struct { q Queue; handlers map[string]Handler }', callers: 0, callees: 0, community: 'queue', caveats: ['risk'] },
  { id: 'web:src/pages/dashboard.tsx#Dashboard',          kind: 'function', name: 'Dashboard',          repo: 'web',      file: 'src/pages/dashboard.tsx:14',    sig: 'export default function Dashboard(): JSX.Element', callers: 0, callees: 22, community: 'ui', caveats: [] },
  { id: 'core-api:internal/middleware/auth.Authn',        kind: 'function', name: 'Authn',              repo: 'core-api', file: 'internal/middleware/auth.go:31', sig: 'func Authn(next http.Handler) http.Handler', callers: 18, callees: 6, community: 'http', caveats: ['hot', 'cycle'] },
]

export type Community = { id: string; name: string; repo: string; symbols: number; files: number; cohesion: number; growth: string }
export const COMMUNITIES: Community[] = [
  { id: 'c-ingest',   name: 'ingest',       repo: 'core-api',  symbols: 624, files: 41, cohesion: 0.71, growth: '+12%' },
  { id: 'c-http',     name: 'http',         repo: 'core-api',  symbols: 324, files: 24, cohesion: 0.75, growth: '+3%' },
  { id: 'c-tucks',    name: 'tucks',        repo: 'tuck_app',  symbols: 167, files: 30, cohesion: 0.55, growth: '+28%' },
  { id: 'c-auth',     name: 'auth',         repo: 'core-api',  symbols: 151, files: 12, cohesion: 0.50, growth: '+4%' },
  { id: 'c-offline',  name: 'offline',      repo: 'tuck_app',  symbols: 145, files: 22, cohesion: 0.66, growth: '+9%' },
  { id: 'c-api',      name: 'api-client',   repo: 'tuck_app',  symbols: 92,  files: 18, cohesion: 0.55, growth: '+6%' },
  { id: 'c-queue',    name: 'queue',        repo: 'worker',    symbols: 77,  files: 11, cohesion: 0.51, growth: '+1%' },
  { id: 'c-store',    name: 'store',        repo: 'core-api',  symbols: 53,  files: 8,  cohesion: 0.42, growth: '-2%' },
  { id: 'c-extract',  name: 'link-ext',     repo: 'core-api',  symbols: 47,  files: 4,  cohesion: 0.81, growth: '0%' },
  { id: 'c-ui',       name: 'ui-tuck-list', repo: 'tuck_app',  symbols: 42,  files: 9,  cohesion: 0.87, growth: '+15%' },
  { id: 'c-proc',     name: 'processor',    repo: 'worker',    symbols: 46,  files: 6,  cohesion: 0.90, growth: '+4%' },
  { id: 'c-core',     name: 'core',         repo: 'extension', symbols: 34,  files: 7,  cohesion: 0.99, growth: '0%' },
]

export type Process = {
  id: string
  name: string
  entry: string
  steps: number
  files: number
  repos: number
  score: number
  risk: 'risk' | 'warn' | 'ok'
  crosses: string[]
}
export const PROCESSES: Process[] = [
  { id: 'p-emailingest', name: 'Email ingest flow',          entry: 'core-api/internal/http/handler.RegisterRoutes → POST /ingest/email', steps: 21,   files: 9,  repos: 3, score: 1460, risk: 'hot' as 'risk', crosses: ['core-api', 'email-worker', 'pkg'] },
  { id: 'p-tucksubmit',  name: 'Tuck submit',                 entry: 'tuck_app/presentation/tuck_screen.submitTuck', steps: 16,   files: 7,  repos: 2, score: 1102, risk: 'warn', crosses: ['tuck_app', 'core-api'] },
  { id: 'p-authlogin',   name: 'Auth / login',                entry: 'web/src/pages/login.tsx → POST /auth/login', steps: 13,   files: 6,  repos: 3, score: 958,  risk: 'ok',   crosses: ['web', 'core-api', 'infra'] },
  { id: 'p-linkextract', name: 'Link extraction',             entry: 'core-api/internal/ingest/email.go → ExtractLinks', steps: 11,   files: 4,  repos: 1, score: 844,  risk: 'warn', crosses: ['core-api'] },
  { id: 'p-offlinesync', name: 'Offline sync',                entry: 'tuck_app/features/offline/sync_service.start', steps: 25,   files: 12, repos: 2, score: 774,  risk: 'risk', crosses: ['tuck_app', 'core-api'] },
  { id: 'p-reduce',      name: 'yy_reduce (sqlite vendored)', entry: 'tuck_app/ios/Pods/sqlite3/shell.c:yy_reduce', steps: 1075, files: 2,  repos: 1, score: 478,  risk: 'warn', crosses: ['tuck_app'] },
  { id: 'p-notifier',    name: 'Push notifier',               entry: 'worker/internal/notifier.Start', steps: 19,   files: 7,  repos: 2, score: 423,  risk: 'ok',   crosses: ['worker', 'core-api'] },
  { id: 'p-queueproc',   name: 'Queue processor loop',        entry: 'worker/internal/queue.Processor.Run', steps: 25,   files: 5,  repos: 1, score: 396,  risk: 'risk', crosses: ['worker'] },
].map(p => ({ ...p, risk: (p.risk === 'hot' ? 'risk' : p.risk) as 'risk' | 'warn' | 'ok' }))

export type Contract = {
  id: string
  name: string
  kind: 'REST' | 'EVENT' | 'URL'
  producer: string
  consumers: string[]
  version: string
  breaking: boolean
  callers: number
  last: string
}
export const CONTRACTS: Contract[] = [
  { id: 'con-auth',   name: 'POST /auth/login',      kind: 'REST',  producer: 'core-api',  consumers: ['web', 'extension'],            version: 'v1.2', breaking: false, callers: 14, last: '2d ago' },
  { id: 'con-ingest', name: 'POST /ingest/email',    kind: 'REST',  producer: 'core-api',  consumers: ['email-worker'],                version: 'v0.9', breaking: true,  callers: 3,  last: '31m ago' },
  { id: 'con-tucks',  name: 'GET /tucks',            kind: 'REST',  producer: 'core-api',  consumers: ['tuck_app', 'web'],             version: 'v1.4', breaking: false, callers: 28, last: '5h ago' },
  { id: 'con-push',   name: 'push.TuckUpdated',      kind: 'EVENT', producer: 'worker',    consumers: ['tuck_app'],                    version: 'v1.0', breaking: false, callers: 11, last: '17m ago' },
  { id: 'con-links',  name: 'link.Extracted',        kind: 'EVENT', producer: 'core-api',  consumers: ['worker', 'email-worker'],      version: 'v2.0', breaking: true,  callers: 9,  last: '1h ago' },
  { id: 'con-share',  name: 'ShareTuck (deep link)', kind: 'URL',   producer: 'extension', consumers: ['tuck_app'],                    version: 'v1.0', breaking: false, callers: 6,  last: '3d ago' },
  { id: 'con-stats',  name: 'GET /admin/stats',      kind: 'REST',  producer: 'core-api',  consumers: ['web'],                         version: 'v1.0', breaking: false, callers: 2,  last: '3d ago' },
]

export type Caveat = {
  id: string
  severity: 'risk' | 'deprecated' | 'hot' | 'unowned' | 'cycle' | 'boundary'
  symbol: string
  title: string
  desc: string
  owner: string
  age: string
}
export const CAVEATS: Caveat[] = [
  { id: 'cv-1', severity: 'risk',       symbol: 'core-api:internal/queue.Processor',         title: 'High fan-in with no tests',     desc: 'Referenced by 14 sites; coverage 0% on hot path.',          owner: 'unassigned',  age: '47d' },
  { id: 'cv-2', severity: 'deprecated', symbol: 'core-api:internal/link.ExtractLinks',       title: 'Deprecated since v0.12',        desc: 'Replaced by `link/v2.Parse`. 9 callers still on old API.',  owner: '@platform',   age: '12d' },
  { id: 'cv-3', severity: 'hot',        symbol: 'core-api:internal/middleware/auth.Authn',   title: 'Hot path on every request',     desc: 'Cyclomatic: 14. Touched in 83% of incidents.',              owner: '@platform',   age: 'ongoing' },
  { id: 'cv-4', severity: 'cycle',      symbol: 'worker:internal/queue.Dispatcher',          title: 'Circular dependency',           desc: 'queue ↔ notifier ↔ queue via registerHandler.',             owner: '@worker-team',age: '3d' },
  { id: 'cv-5', severity: 'unowned',    symbol: 'tuck_app:lib/features/tucks.TuckRepository',title: 'No CODEOWNERS match',           desc: 'Last author left org; 28 open PRs touch this file.',        owner: 'unassigned',  age: '91d' },
  { id: 'cv-6', severity: 'boundary',   symbol: 'core-api:/ingest/email',                    title: 'Breaking API change pending',   desc: 'v0.9 → v1.0 removes `raw_headers` field. 3 consumers.',     owner: '@platform',   age: 'tomorrow' },
  { id: 'cv-7', severity: 'risk',       symbol: 'tuck_app:features/offline/sync_service',    title: 'Race on retry counter',         desc: 'Two goroutines (isolates) increment without lock.',         owner: '@mobile',     age: '8d' },
]

export type Activity = { t: string; actor: string; msg: string; kind: 'info' | 'warn' | 'ok' }
export const ACTIVITY: Activity[] = [
  { t: 'just now', actor: 'watch', msg: 're-indexed core-api/internal/ingest/email.go',     kind: 'info' },
  { t: '2m',       actor: '@sam',  msg: 'pinned investigation "Email bounce 500s"',          kind: 'info' },
  { t: '17m',      actor: 'guard', msg: 'boundary rule violated: web → core-api/internal/*', kind: 'warn' },
  { t: '41m',      actor: '@ira',  msg: 'added guard `no-import-internal-from-ui`',          kind: 'info' },
  { t: '1h',       actor: 'ci',    msg: 'blast radius grew +18% after PR #2841',             kind: 'warn' },
  { t: '2h',       actor: '@sam',  msg: 'resolved cycle in worker.queue',                    kind: 'ok' },
  { t: 'yday',     actor: 'index', msg: 'added 62 symbols from pkg/',                        kind: 'info' },
]

export type Guard = { id: string; name: string; kind: string; status: 'violated' | 'warn' | 'ok'; hits: number; scope: string }
export const GUARDS: Guard[] = [
  { id: 'g-1', name: 'no-import-internal-from-ui', kind: 'boundary',  status: 'violated', hits: 3, scope: 'web → core-api/internal/**' },
  { id: 'g-2', name: 'co-change-store-migrations', kind: 'co-change', status: 'ok',       hits: 0, scope: 'store/*.go ↔ migrations/*.sql' },
  { id: 'g-3', name: 'no-cycle-auth-queue',        kind: 'cycle',     status: 'violated', hits: 1, scope: 'auth ↔ queue' },
  { id: 'g-4', name: 'contract-version-bump',      kind: 'contract',  status: 'ok',       hits: 0, scope: 'breaking change requires /v{N+1}' },
  { id: 'g-5', name: 'owner-required-on-public',   kind: 'ownership', status: 'warn',     hits: 2, scope: 'exported symbols require owner tag' },
]

export const RECENT_SEARCHES = [
  { q: 'ExtractLinks',   kind: 'function',  hits: 5 },
  { q: 'TuckRepository', kind: 'type',      hits: 12 },
  { q: 'RegisterRoutes', kind: 'function',  hits: 3 },
  { q: 'kind:interface', kind: 'facet',     hits: 412 },
]

export const FAKE_SPARK: Record<string, number[]> = {
  'core-api':     [5, 6, 5, 7, 8, 6, 7, 9, 8, 10, 9, 11],
  tuck_app:       [30, 32, 35, 38, 40, 42, 45, 48, 52, 54, 56, 58],
  worker:         [4, 5, 5, 4, 6, 7, 6, 8, 9, 8, 10, 9],
  web:            [10, 11, 12, 11, 13, 14, 13, 15, 16, 15, 17, 16],
  infra:          [2, 3, 3, 3, 4, 4, 4, 5, 5, 5, 6, 6],
  extension:      [3, 4, 4, 5, 5, 6, 6, 6, 7, 7, 7, 8],
  'email-worker': [1, 1, 2, 2, 2, 3, 3, 3, 3, 3, 3, 3],
  pkg:            [1, 1, 1, 1, 1, 1, 2, 2, 2, 2, 2, 2],
}
