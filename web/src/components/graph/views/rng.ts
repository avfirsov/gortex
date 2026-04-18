export function rnd(seed: number) {
  let s = seed
  return () => {
    s = (s * 1664525 + 1013904223) >>> 0
    return (s & 0xfffffff) / 0xfffffff
  }
}

export function sampleSymbolName(r: () => number) {
  const pool = [
    'handleEmailIngest', 'RegisterRoutes', 'ExtractLinks', 'TuckRepository',
    'syncTucks', 'Processor', 'Dispatcher', 'AuthMiddleware', 'PostgresStore',
    'renderList', 'loadConfig', 'httpServer', 'buildIndex',
  ]
  return pool[Math.floor(r() * pool.length)]
}
