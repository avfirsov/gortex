import { Injectable } from '@angular/core';
import { User } from './user.model';

// Modern Angular `providedIn: 'root'` — makes the service available
// app-wide without a NgModule providers array. The service is a plain
// TypeScript class with a few methods agents would want to trace from
// their call sites.
@Injectable({ providedIn: 'root' })
export class UsersService {
  private readonly store = new Map<string, User>();

  findOne(id: string): User | undefined {
    return this.store.get(id);
  }

  findAll(): User[] {
    return Array.from(this.store.values());
  }

  create(email: string): User {
    const id = `usr_${this.store.size + 1}`;
    const u: User = { id, email };
    this.store.set(id, u);
    return u;
  }
}
