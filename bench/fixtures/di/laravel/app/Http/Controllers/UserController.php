<?php

namespace App\Http\Controllers;

use App\Http\Middleware\AdminMiddleware;
use App\Http\Middleware\AuthenticateMiddleware;
use App\Repositories\UserRepository;
use App\Services\Clock;

class UserController
{
    // Constructor-injected typed dependencies. Laravel's container
    // autowires from the type hints; UserRepository resolves through
    // the binding in AppServiceProvider::register() to
    // EloquentUserRepository.
    public function __construct(
        private UserRepository $users,
        private Clock $clock,
    ) {
        // Controller middleware: registered imperatively in the
        // constructor. 'auth' applies to every action; 'admin' is
        // filtered with ->only(['destroy']), same shape as Rails
        // before_action + only:.
        $this->middleware(AuthenticateMiddleware::class);
        $this->middleware(AdminMiddleware::class)->only(['destroy']);
    }

    public function index(): array
    {
        return $this->users->all();
    }

    public function show(string $id): ?array
    {
        return $this->users->find($id);
    }

    public function destroy(string $id): string
    {
        return 'deleted at ' . $this->clock->now();
    }
}
