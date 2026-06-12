import { Injectable, inject } from '@angular/core';
import { UsersService } from '../users/users.service';

// Second service with a findOne() method — creates a name-collision
// with UsersService.findOne. A resolver that picks up the class field
// `this.users = inject(UsersService)` as UsersService-typed will
// correctly route `this.users.findOne(...)` to UsersService. A
// name-only fallback would arbitrarily pick one of the two findOne
// implementations.
@Injectable({ providedIn: 'root' })
export class SessionService {
  private readonly users = inject(UsersService);

  // Same method name as UsersService — deliberate collision so the
  // call `this.users.findOne(...)` forces type-aware resolution.
  findOne(sessionId: string): { userId: string } | undefined {
    return { userId: sessionId };
  }

  currentUser(sessionId: string): string | undefined {
    const s = this.findOne(sessionId);
    if (!s) return undefined;
    return this.users.findOne(s.userId)?.email;
  }
}
