<?php

namespace App\Providers;

use App\Repositories\EloquentUserRepository;
use App\Repositories\UserRepository;
use App\Services\Clock;
use Illuminate\Support\ServiceProvider;

class AppServiceProvider extends ServiceProvider
{
    // register() is Laravel's container-binding entry point. Every
    // bind/singleton/instance call here declares that when someone
    // asks the container for the first argument, give them an
    // instance produced from the second. These bindings are the DI
    // gap that matters — consumers typed against the interface won't
    // reach the implementation without this info.
    public function register(): void
    {
        // bind: a fresh EloquentUserRepository per resolve.
        $this->app->bind(UserRepository::class, EloquentUserRepository::class);

        // singleton: one Clock shared across the app lifetime.
        $this->app->singleton(Clock::class, function ($app) {
            return new Clock('UTC');
        });
    }
}
