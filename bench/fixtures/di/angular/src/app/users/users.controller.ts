import { Component, inject } from '@angular/core';
import { UsersService } from './users.service';
import { AuthService } from '../auth/auth.service';

// A plain-ish component-like class that mixes both DI styles:
//  - `inject(UsersService)` — function form
//  - constructor parameter-property — classic Angular (pre-14)
@Component({
  selector: 'users-list',
  template: '',
})
export class UsersListComponent {
  private readonly users = inject(UsersService);

  constructor(private readonly auth: AuthService) {}

  displayList(): string[] {
    return this.users.findAll().map((u) => u.email);
  }

  validateAndList(userId: string, token: string): string[] {
    this.auth.validate(userId, token);
    return this.displayList();
  }
}
