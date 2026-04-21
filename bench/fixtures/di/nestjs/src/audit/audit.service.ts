import { Inject, Injectable } from '@nestjs/common';
import { UsersService } from '../users/users.service';
import { DATABASE_URL } from '../config/config.tokens';

// Property injection: NestJS supports @Inject on class fields for cases
// where the class can't declare a constructor (or the author prefers it
// over parameter properties). Two shapes exercised:
//  - `@Inject() field!: T`     — implicit token, class type drives binding
//  - `@Inject(TOKEN) field: ...` — explicit token, same as constructor form
@Injectable()
export class AuditService {
  @Inject()
  private readonly users!: UsersService;

  @Inject(DATABASE_URL)
  private readonly dbUrl!: string;

  async recordLogin(userId: string): Promise<string> {
    const user = await this.users.findOne(userId);
    return `audit(${this.dbUrl}): login by ${user.id}`;
  }
}
