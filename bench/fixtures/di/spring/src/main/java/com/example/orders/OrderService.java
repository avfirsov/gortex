package com.example.orders;

import java.time.Clock;
import java.time.Instant;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.stereotype.Service;

import com.example.users.User;
import com.example.users.UserService;

// Multi-field injection by constructor. The Clock dependency is
// satisfied by the @Bean method in com.example.config.Clocks; the
// UserService dependency is the typical @Service-annotated class.
@Service
public class OrderService {
    private final UserService users;
    private final Clock clock;

    @Autowired
    public OrderService(UserService users, Clock clock) {
        this.users = users;
        this.clock = clock;
    }

    public String describe(String userId) {
        User u = users.lookup(userId).orElseThrow();
        Instant now = clock.instant();
        return "order for " + u.getEmail() + " at " + now;
    }
}
