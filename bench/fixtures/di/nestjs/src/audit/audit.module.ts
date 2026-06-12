import { Module } from '@nestjs/common';
import { UsersModule } from '../users/users.module';
import { ConfigModule } from '../config/config.module';
import { AuditService } from './audit.service';

@Module({
  imports: [UsersModule, ConfigModule],
  providers: [AuditService],
  exports: [AuditService],
})
export class AuditModule {}
