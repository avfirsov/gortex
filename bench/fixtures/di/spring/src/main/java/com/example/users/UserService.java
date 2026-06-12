package com.example.users;

import java.util.Optional;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.beans.factory.annotation.Qualifier;
import org.springframework.stereotype.Service;

// Mixed injection styles: field injection for the primary repository
// and constructor injection for the qualified in-memory one. Both are
// common in production Spring code.
@Service
public class UserService {
    @Autowired
    private UserRepository primary;

    private final InMemoryUserRepository cache;

    @Autowired
    public UserService(@Qualifier("inMemoryUsers") InMemoryUserRepository cache) {
        this.cache = cache;
    }

    public Optional<User> lookup(String id) {
        Optional<User> hit = cache.findById(id);
        if (hit.isPresent()) {
            return hit;
        }
        return primary.findById(id);
    }
}
