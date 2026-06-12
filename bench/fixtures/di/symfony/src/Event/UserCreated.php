<?php

namespace App\Event;

// Domain event fired when a user is registered. Listeners are bound
// via #[AsEventListener(event: UserCreated::class)] on their methods;
// Symfony's dispatcher routes the event instance to each listener.
class UserCreated
{
    public function __construct(public readonly string $userId) {}
}
