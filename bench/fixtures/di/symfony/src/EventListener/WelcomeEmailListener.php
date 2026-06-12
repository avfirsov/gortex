<?php

namespace App\EventListener;

use App\Event\UserCreated;
use App\Event\UserDeleted;
use Symfony\Component\EventDispatcher\Attribute\AsEventListener;

// Method-level attributes. Each listener method declares exactly
// which event it handles. Symfony dispatches the right method when
// the event is fired — no explicit call site from anywhere, the
// attribute IS the binding.
class WelcomeEmailListener
{
    #[AsEventListener(event: UserCreated::class, priority: 10)]
    public function onCreated(UserCreated $event): void
    {
        // Normally sends an email; body is a stub for this fixture.
        $_ = $event->userId;
    }

    #[AsEventListener(event: UserDeleted::class)]
    public function onDeleted(UserDeleted $event): void
    {
        $_ = $event->userId;
    }
}
