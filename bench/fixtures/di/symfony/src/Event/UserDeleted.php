<?php

namespace App\Event;

class UserDeleted
{
    public function __construct(public readonly string $userId) {}
}
