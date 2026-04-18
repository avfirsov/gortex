export type NavItem = {
  id: string
  href: string
  label: string
  icon: string
  num?: string | null
  kbd?: string
}

export const NAV: NavItem[] = [
  { id: 'dashboard',     href: '/',                label: 'Dashboard',     icon: 'dash',    num: null },
  { id: 'graph',         href: '/graph',           label: 'Graph',         icon: 'graph',   num: '13,963' },
  { id: 'search',        href: '/search',          label: 'Search',        icon: 'search',  num: null, kbd: '⌘K' },
  { id: 'communities',   href: '/communities',     label: 'Communities',   icon: 'users',   num: '384' },
  { id: 'processes',     href: '/processes',       label: 'Processes',     icon: 'route',   num: '127' },
  { id: 'contracts',     href: '/contracts',       label: 'Contracts',     icon: 'plug',    num: '188' },
  { id: 'services',      href: '/services',        label: 'Services',      icon: 'service', num: '8' },
  { id: 'investigations',href: '/investigations',  label: 'Investigations',icon: 'flask',   num: '6' },
  { id: 'caveats',       href: '/caveats',         label: 'Caveats',       icon: 'warn',    num: '42' },
  { id: 'guards',        href: '/guards',          label: 'Guards',        icon: 'beaker',  num: '5' },
]

export const PAGE_CRUMBS: Record<string, { label: string; href?: string }[]> = {
  dashboard:      [{ label: 'Gortex', href: '/' }, { label: 'Dashboard' }],
  graph:          [{ label: 'Gortex', href: '/' }, { label: 'Graph' }],
  search:         [{ label: 'Gortex', href: '/' }, { label: 'Search' }],
  communities:    [{ label: 'Gortex', href: '/' }, { label: 'Communities' }],
  processes:      [{ label: 'Gortex', href: '/' }, { label: 'Processes' }],
  contracts:      [{ label: 'Gortex', href: '/' }, { label: 'Contracts' }],
  services:       [{ label: 'Gortex', href: '/' }, { label: 'Services' }],
  investigations: [{ label: 'Gortex', href: '/' }, { label: 'Investigations' }, { label: 'Email ingest 500s' }],
  caveats:        [{ label: 'Gortex', href: '/' }, { label: 'Caveats' }],
  guards:         [{ label: 'Gortex', href: '/' }, { label: 'Guards' }],
  symbol:         [{ label: 'Gortex', href: '/' }, { label: 'Symbol' }],
}
