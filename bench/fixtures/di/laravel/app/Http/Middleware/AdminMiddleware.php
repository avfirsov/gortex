<?php

namespace App\Http\Middleware;

use Closure;

class AdminMiddleware
{
    public function handle($request, Closure $next)
    {
        if (empty($request->user['admin'])) {
            return response('forbidden', 403);
        }
        return $next($request);
    }
}
