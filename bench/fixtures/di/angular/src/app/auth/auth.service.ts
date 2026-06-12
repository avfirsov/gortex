import { Injectable, inject } from '@angular/core';
import { UsersService } from '../users/users.service';

// Modern Angular: `inject()` function-style DI. No constructor required
// — the service is resolved via Angular's injector context at field-
// initialization time. This is the shape ordinary static analysis
// misses entirely, because the dependency edge lives inside an
// argument of a generic-looking function call (`inject(X)`) rather
// than in a typed constructor param.
@Injectable({ providedIn: 'root' })
export class AuthService {
  private readonly users = inject(UsersService);

  validate(userId: string, token: string): boolean {
    const u = this.users.findOne(userId);
    return u !== undefined && token === `tok_${u.id}`;
  }
}
