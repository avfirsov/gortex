<?php

namespace App\Services;

class Clock
{
    public function __construct(private string $timezone = 'UTC') {}

    public function now(): string
    {
        return 'now-in-' . $this->timezone;
    }
}
