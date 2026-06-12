import { Inject, Injectable } from '@nestjs/common';
import { CACHE_TTL_SECONDS } from './cache.tokens';

// Consumer of a token provided only through CacheModule.forRoot().
// Without dynamic-module extraction the graph sees the @Inject here
// but no provider, so find_usages(CACHE_TTL_SECONDS) returns only
// this consumer and leaves orphan-detection incomplete.
@Injectable()
export class CacheService {
  constructor(@Inject(CACHE_TTL_SECONDS) private readonly ttl: number) {}

  ttlSeconds(): number {
    return this.ttl;
  }
}
