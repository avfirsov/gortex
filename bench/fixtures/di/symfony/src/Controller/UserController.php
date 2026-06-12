<?php

namespace App\Controller;

use App\Event\UserCreated;
use Symfony\Component\EventDispatcher\EventDispatcherInterface;
use Symfony\Component\HttpFoundation\Response;
use Symfony\Component\Routing\Attribute\Route;

// Standard Symfony controller. Constructor-injected dispatcher +
// route attributes. The event dispatch in register() will fire every
// listener bound to UserCreated — the listener bindings live in the
// #[AsEventListener] attributes in the EventListener directory.
class UserController
{
    public function __construct(
        private EventDispatcherInterface $events,
    ) {}

    #[Route('/users', methods: ['POST'])]
    public function register(): Response
    {
        $event = new UserCreated('usr_1');
        $this->events->dispatch($event);
        return new Response('ok');
    }
}
