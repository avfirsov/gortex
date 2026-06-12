import { DynamicModule, Module } from '@nestjs/common';
import { CACHE_TTL_SECONDS } from './cache.tokens';
import { CacheService } from './cache.service';

// Dynamic module: NestJS's forRoot / forFeature pattern returns a
// runtime-computed module config. The providers array here is
// structurally identical to a @Module providers array — same
// { provide: X, useValue: ... } shape — but lives inside a static
// method body instead of a decorator.
@Module({})
export class CacheModule {
  static forRoot(ttlSeconds: number): DynamicModule {
    return {
      module: CacheModule,
      providers: [
        { provide: CACHE_TTL_SECONDS, useValue: ttlSeconds },
        CacheService,
      ],
      exports: [CacheService],
    };
  }
}
