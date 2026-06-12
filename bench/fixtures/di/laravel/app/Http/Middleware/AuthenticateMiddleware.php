<?php

namespace App\Http\Middleware;

use Closure;

// Laravel middleware. handle() is what the framework calls before the
// controller action runs. There's no explicit call site in the
// controller or route definition — the middleware is referenced by
// its short alias ("auth") or class name in a call like
// $this->middleware('auth').
class AuthenticateMiddleware
{
    public function handle($request, Closure $next)
    {
        if (empty($request->user)) {
            return response('unauthorized', 401);
        }
        return $next($request);
    }
}
