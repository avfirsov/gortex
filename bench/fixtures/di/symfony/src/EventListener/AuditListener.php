<?php

namespace App\EventListener;

use App\Event\UserCreated;
use Symfony\Component\EventDispatcher\Attribute\AsEventListener;

// Class-level attribute shorthand. Symfony scans the class for a
// method whose signature matches the event type; here that's
// __invoke taking UserCreated. This is a shorter spelling of the
// method-level form above.
#[AsEventListener(event: UserCreated::class)]
class AuditListener
{
    public function __invoke(UserCreated $event): void
    {
        $_ = $event->userId;
    }
}
