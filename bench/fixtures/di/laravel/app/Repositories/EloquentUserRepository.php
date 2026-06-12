<?php

namespace App\Repositories;

// Concrete UserRepository. Service provider binds this to the
// interface — without that binding, the Laravel container has no way
// to pick an implementation from the interface alone. Same shape as
// NestJS useClass / Spring @Bean return-type binding.
class EloquentUserRepository implements UserRepository
{
    public function find(string $id): ?array
    {
        return ['id' => $id];
    }

    public function all(): array
    {
        return [];
    }
}
