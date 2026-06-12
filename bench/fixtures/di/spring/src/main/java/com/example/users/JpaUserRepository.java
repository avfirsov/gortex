package com.example.users;

import java.util.Optional;
import org.springframework.stereotype.Repository;

// First implementation of UserRepository. The @Primary elsewhere
// determines which Spring picks when a consumer @Autowires the
// interface without a qualifier.
@Repository
public class JpaUserRepository implements UserRepository {
    public Optional<User> findById(String id) {
        return Optional.empty();
    }

    public User save(User u) {
        return u;
    }
}
