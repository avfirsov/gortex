<?php

namespace App\Repositories;

interface UserRepository
{
    public function find(string $id): ?array;
    public function all(): array;
}
